# 群聊第一步教程：创建、查询、列表与资料更新

这篇教程对应开发清单的 `GROUP-001`。目标不只是告诉你“代码写在哪”，而是让你理解一个后端业务从 HTTP 请求到数据库落库，为什么要这样分层、怎样保证不会留下半成品数据，以及前端怎样真正用起来。

本阶段已经完成四个接口：

| 功能 | 方法与路径 | 成功时的 `data` |
| --- | --- | --- |
| 创建群 | `POST /api/v1/group` | `{ "group_id": 123 }` |
| 我的群列表 | `GET /api/v1/group/list` | `Group[]`，没有群时为 `[]` |
| 群详情 | `GET /api/v1/group/:groupID` | `Group` |
| 更新资料 | `PUT /api/v1/group/:groupID` | 省略 `data` |

四个接口都需要登录后的 access token。成员邀请、成员列表、角色修改、退群和转让群主属于 `GROUP-002～004`，本阶段仍会明确返回 501。

## 1. 先建立一个正确的心智模型

一次“创建群”的请求会依次经过：

```text
浏览器
  -> Gin Router
  -> JWT 中间件
  -> GroupHandler
  -> GroupProfileService
  -> GroupRepository
  -> MySQL
  -> 提交后刷新 Redis 投影
```

可以把这些层想成餐厅：

- Router 像门牌，决定请求应该去哪个窗口。
- JWT 中间件像验票员，确认“你是谁”。
- Handler 像前台，只负责接收 JSON、读取路径参数、返回统一响应。
- Service 像店长，决定业务规则和操作顺序。
- Repository 像仓库管理员，只负责怎样读写 MySQL。
- MySQL 是权威账本；Redis 是为了后续群消息检查更快而建立的副本。

分层不是为了把代码拆得越碎越好。它解决的是三个具体问题：HTTP 细节不会污染业务规则；业务测试不需要启动数据库；SQL 变化不会迫使前端处理层跟着重写。

## 2. 为什么群需要两张表

群本身和“谁在群里”是两类数据：

```text
groups
  id=100, name=学习小组, owner_id=7

group_members
  group_id=100, user_id=7, role=2
  group_id=100, user_id=8, role=0
```

`groups` 保存群资料；`group_members` 保存多对多成员关系。一个用户可以加入多个群，一个群也可以拥有多个用户，所以不能把全部成员 ID 塞进 `groups` 的一个字符串字段。

角色常量位于 [group.go](../backend/internal/model/group.go)：

- `GroupRoleMember = 0`：普通成员
- `GroupRoleAdmin = 1`：管理员
- `GroupRoleOwner = 2`：群主

创建完成后必须满足一个核心不变量：

```text
groups.owner_id == 群主成员行的 user_id
并且该成员行 role == 2
```

`owner_id` 便于快速判断真实群主，`role=2` 便于在成员列表里展示身份。两处表达同一个事实，就必须通过事务一起维护。

## 3. 最重要的概念：创建事务

假设不使用事务，程序先插入 `groups`，随后插入群主成员时数据库断开：

```text
groups 有群 100
group_members 没有群 100 的群主
```

这个群无法正常管理，称为“半成品数据”或“孤儿群”。事务把多个 SQL 包成一个不可分割的整体：

```text
BEGIN
  确认创建者存在并锁住用户行
  INSERT groups
  INSERT group_members(role=2)
  INSERT cache_reconcile_events
COMMIT
```

任何一步失败就 `ROLLBACK`，三项都不存在；全部成功才 `COMMIT`，三项一起可见。

具体入口在 [group.go](../backend/internal/service/group.go)，事务实现位于 [group_repository.go](../backend/internal/repository/group_repository.go)。Service 依赖的是窄 `GroupRepository` 接口，不依赖 `*sql.DB`，因此单元测试可以用内存 fake 精确模拟“第二步失败时是否回滚”。

为什么创建前还要锁用户行？JWT 证明 token 曾经合法，不代表该用户此刻仍存在。锁定并确认用户行，可以避免用户删除与建群并发时产生悬空的 `owner_id`；项目当前没有靠外键替应用兜底，所以这一步尤其重要。

## 4. 创建接口逐层拆解

请求示例：

```json
{
  "name": "学习小组",
  "notice": "每天交流一个 Go 知识点"
}
```

### 4.1 Handler 做什么

[group_handler.go](../backend/internal/api/group_handler.go) 的创建方法只做四件事：

1. 从 JWT 中间件写入的 Context 取得当前 `userID`。
2. 用 `ShouldBindJSON` 把 JSON 绑定到 `CreateGroupRequest`。
3. 调用 `GroupProfileService.Create`。
4. 成功后返回 HTTP 201 和 `{group_id}`，失败交给统一错误映射。

Handler 不判断“群名最多多少字符”，因为同一业务未来可能被 HTTP、后台任务或管理命令复用。业务规则只放一份，应该在 Service。

### 4.2 Service 做什么

Service 先校验并归一化资料：

- 群名会去掉首尾空白，不能为空，最多 50 个 Unicode 字符。
- 群名不能包含换行等控制字符。
- 公告会去掉首尾空白，最多 300 个 Unicode 字符。
- 公告允许正常的换行和制表符，但拒绝 NUL 等不可见控制字符。

这里数的是“字符”而不是 UTF-8 字节。`学习群` 是 3 个字符，不会因为每个汉字占多个字节而被误算。

校验通过后，Service 编排前面讲过的事务。成功提交后才返回数据库生成的群 ID。

### 4.3 Repository 做什么

Repository 使用参数占位符执行 SQL：

```sql
INSERT INTO `groups` (name, notice, owner_id, max_members, created_at, updated_at)
VALUES (?, ?, ?, 500, NOW(), NOW());
```

`?` 的值由驱动单独传递，不是字符串拼接，因此名字里即使有引号，也不会变成 SQL 注入。

## 5. “详情”和“列表”为什么都要检查成员关系

### 5.1 我的群列表

正确查询是从成员关系反查：

```sql
SELECT g.*
FROM group_members gm
JOIN `groups` g ON g.id = gm.group_id
WHERE gm.user_id = ?;
```

不能只查询 `groups.owner_id = 当前用户`，因为那只会得到“我创建的群”，看不到“我后来加入的群”。也不能直接读取 Redis 的 `user_groups:{uid}`：Redis 是可重建投影，预热过程中可能暂时不完整，而且这个键没有承担权威列表查询的契约。

Service 会把 Repository 返回的 `nil` 规范为非 nil 空切片，所以 JSON 一定是 `[]`，不会是 `null`。现有前端会直接遍历这个结果，这个细节属于接口契约的一部分。

### 5.2 群详情

程序先确认群存在，再确认当前用户有对应 `group_members` 行：

- 群不存在：HTTP 404，业务码 `1302`。
- 群存在但不是成员：HTTP 403，业务码 `5001`。
- 是成员：返回完整群资料。

这样其他登录用户不能只靠猜测自增 ID 就读取陌生群的公告。

查询里的 `COALESCE(notice, '')` 还处理了历史或人工写入的 NULL。Go 模型的 `Notice` 是 string，接口契约也要求返回空字符串而非 `null`。

## 6. 更新资料的权限与并发

更新权限矩阵是：

| 操作者 | 能否更新 name/notice |
| --- | --- |
| `groups.owner_id` 指向的真实群主 | 可以 |
| `group_members.role=1` 的管理员 | 可以 |
| 普通成员 role=0 | 不可以，返回 1301 |
| 非成员 | 不可以，返回 1301 |
| 不是 owner_id、但脏数据里意外 role=2 的用户 | 不可以 |

最后一行很重要。数据库目前不能用简单唯一索引保证每群只有一条 `role=2`，所以权限不能盲信任任意 role=2；真实群主以 `groups.owner_id` 为准。

更新在事务中按固定顺序加锁：

```text
SELECT groups ... FOR UPDATE
  -> SELECT 当前成员 ... FOR UPDATE
  -> 检查 owner_id 或 role=1
  -> UPDATE groups SET name=?, notice=?
```

所有后续群成员和群主变更也应保持“先锁群、再锁成员”的顺序，降低并发死锁风险。更新 SQL只改 `name/notice`，绝不复用会同时写 `owner_id` 的旧通用方法，否则一个普通资料编辑就可能破坏群主不变量。

重复提交相同内容也算成功。MySQL 对“值没有实际变化”的 UPDATE 可能报告 0 行变化，所以这里不能把 `RowsAffected()==0` 当作群不存在。

## 7. MySQL 是真相，Redis 是可重建投影

后续发送群消息时，Lua 需要快速知道发送者是否是成员、角色是什么、是否被禁言。Redis 为此保存：

- `group_members:{gid}`：成员 ID Set
- `group_member_info:{gid}`：角色与禁言信息 Hash
- `user_groups:{uid}`：用户到群的反向 Set

建群事务除群和群主外，还插入一条 `cache_reconcile_events`。提交后 Service 调用 `ReconcileGroupMembers`，重新从 MySQL 读取完整成员状态并原子覆盖这些 Redis 键。

如果 Redis 恰好不可用：

```text
MySQL 已 COMMIT
Redis 快速刷新失败
HTTP 仍返回建群成功
后台 Reconciler 根据事件重试
```

这是诚实的错误语义。不能告诉客户端“创建失败”，因为 MySQL 里群已经真实存在；客户端重试反而可能创建第二个群。协调事件和核心数据在同一事务中，保证成功落库的状态一定留下可重试凭据。

## 8. 为什么又增加了 GroupProfileService

原来的 `GroupService` 同时声明了 GROUP-001～004 的全部方法。如果 Handler 直接依赖它，实现第一步时就被迫为成员邀请、角色、转让群主等未完成功能写假方法。

[contracts.go](../backend/internal/service/contracts.go) 现在把当前能力拆成：

```text
GroupProfileService       // 已完成的四个资料用例
GroupService              // 嵌入上面接口，再声明后续成员/角色用例
```

这叫接口隔离：调用者只依赖它真正使用的能力。未实现的成员路由继续由 Router 返回结构化 501，而不是用“return nil”的空实现假装完成。

## 9. 前端怎样接上这四个接口

前端 API 已在 [groups.ts](../frontend/src/api/groups.ts) 中定义。本次还补了三处真正影响使用的衔接：

1. 登录初始化会主动请求 `/group/list`，所以刷新页面后能恢复群会话，不必等待尚未完成的离线消息同步。
2. 即使 GROUP-002 的成员列表暂未实现，当前用户只要等于 `group.owner_id`，也能看到“编辑资料”按钮。
3. 改名成功会同步更新本地会话标题，不需要刷新页面。

前端仍不会提前开放邀请成员、设管理员或退群逻辑；这些能力要等对应后端任务完成。

## 10. 自己动手调用接口

先启动依赖和服务，并完成注册/登录。拿到 access token 后，在 PowerShell 中设置：

```powershell
$base = 'http://localhost:8080/api/v1'
$accessToken = '把登录返回的 access_token 放这里'
$headers = @{ Authorization = "Bearer $accessToken" }
```

创建群：

```powershell
$created = Invoke-RestMethod -Method Post -Uri "$base/group" `
  -Headers $headers -ContentType 'application/json' `
  -Body '{"name":"Go 学习小组","notice":"每天进步一点"}'
$groupID = $created.data.group_id
$created
```

查看我的群列表和详情：

```powershell
Invoke-RestMethod -Method Get -Uri "$base/group/list" -Headers $headers
Invoke-RestMethod -Method Get -Uri "$base/group/$groupID" -Headers $headers
```

更新资料：

```powershell
Invoke-RestMethod -Method Put -Uri "$base/group/$groupID" `
  -Headers $headers -ContentType 'application/json' `
  -Body '{"name":"Go 进阶小组","notice":"从并发与网络开始"}'
```

也可以在 MySQL 中观察不变量：

```sql
SELECT id, name, notice, owner_id FROM `groups` WHERE id = 你的群ID;
SELECT group_id, user_id, role FROM group_members WHERE group_id = 你的群ID;
SELECT resource_type, resource_id, status
FROM cache_reconcile_events
WHERE resource_type = 'group_members' AND resource_id = 你的群ID;
```

你应该看到 `groups.owner_id` 与 `role=2` 成员的 `user_id` 相同。

## 11. 测试怎样证明实现可信

测试分三层：

- [group_test.go](../backend/internal/service/group_test.go)：内存 fake 验证事务回滚、权限矩阵、校验边界、Redis 失败兜底。
- [group_handler_test.go](../backend/internal/api/group_handler_test.go)：真实 JWT + HTTP Router 验证状态码、统一信封、参数绑定和空数组。
- [group_integration_test.go](../backend/internal/service/group_integration_test.go)：真实 MySQL 验证 SQL、事务、JOIN 列表和权限。
- 前端 group 测试：验证登录恢复群列表、跨用户请求竞态、群主编辑按钮和改名同步。

日常快速验证：

```powershell
Set-Location 'E:\IT\my_IM\backend'
go test ./...

Set-Location 'E:\IT\my_IM\frontend'
npm.cmd run typecheck
npm.cmd test
```

真实 MySQL 集成测试需要先启动项目 Docker 依赖：

```powershell
$env:MYIM_INTEGRATION = '1'
go test ./internal/service -run '^TestGroupDockerIntegration$' -count=1 -v
```

集成测试使用唯一用户名和精确 ID 清理，不会 `TRUNCATE` 或清空你的整库数据。

## 12. 本阶段的文件地图

| 文件 | 作用 |
| --- | --- |
| `backend/internal/api/group_handler.go` | HTTP 参数、调用 Service、统一响应 |
| `backend/internal/service/group.go` | 校验、权限、事务流程、缓存刷新 |
| `backend/internal/repository/group_repository.go` | 窄接口、事务和群查询 SQL |
| `backend/internal/model/group.go` | 群模型与角色常量 |
| `backend/cmd/server/main.go` | 创建 GroupService 并装入 Router |
| `frontend/src/components/realtime/RealtimeBootstrap.tsx` | 登录后恢复我的群列表 |
| `frontend/src/features/groups/GroupManagement.tsx` | 群主编辑与改名后同步 UI |

学习这一步时，建议按 `Handler -> Service -> Repository -> Test` 的顺序阅读。先看请求怎样进入，再看业务规则怎样决定，最后看 SQL 怎样实现；测试则告诉你每一层承诺了什么。
