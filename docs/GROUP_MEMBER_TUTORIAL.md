# 群聊第二步教程：添加、移除与分页查询成员

这篇教程对应开发清单中的 `GROUP-002`。读完后，你应该不只是会调用三个接口，还能明白：

- 为什么“邀请好友入群”必须放在事务里；
- 为什么检查人数后再插入，仍然可能超员，以及怎样用锁解决；
- 群主和管理员分别能移除谁；
- 为什么 MySQL 成功、Redis 暂时失败时，接口仍应返回成功；
- 后端分页和前端“收齐所有页”是怎样配合的。

本阶段完成三个需要 access token 的接口：

| 功能 | 方法与路径 | 请求/查询参数 | 成功结果 |
| --- | --- | --- | --- |
| 邀请成员 | `POST /api/v1/group/:groupID/member` | JSON：`{"member_id": 8}` | HTTP 200，省略 `data` |
| 移除成员 | `DELETE /api/v1/group/:groupID/member/:memberID` | 路径中的成员用户 ID | HTTP 200，省略 `data` |
| 成员列表 | `GET /api/v1/group/:groupID/members` | `limit=20&offset=0` | 分页成员资料 |

角色修改与禁言后来已经在 `GROUP-003` 完成，详见 [GROUP_ROLE_MUTE_TUTORIAL.md](GROUP_ROLE_MUTE_TUTORIAL.md)；退群和转让群主仍属于 `GROUP-004`。本篇继续只讲 GROUP-002 的边界。

## 1. 先把数据关系想清楚

群资料与群成员分别保存在两张表：

```text
groups
  id=100, owner_id=7, max_members=500

group_members
  group_id=100, user_id=7, role=2   // 群主
  group_id=100, user_id=8, role=1   // 管理员
  group_id=100, user_id=9, role=0   // 普通成员
```

`group_members` 的 `(group_id, user_id)` 有唯一索引，所以同一个用户不能在同一个群里出现两次。

成员列表还需要昵称和头像，但这两个字段属于 `users`。Repository 因此用 JOIN 一次查齐：

```sql
SELECT gm.*, u.username, COALESCE(u.avatar_url, '')
FROM group_members gm
JOIN users u ON u.id = gm.user_id
WHERE gm.group_id = ?
ORDER BY gm.role DESC, gm.joined_at ASC, gm.id ASC
LIMIT ? OFFSET ?;
```

排序规则是：角色高的在前，同角色中先入群的在前，最后用自增 ID 保证顺序稳定。

## 2. 先写规则表，再写 if

权限代码最容易写成一堆互相矛盾的 `if`。更稳妥的办法是先把规则写成表。

### 2.1 邀请权限

| 操作者 | 能否邀请自己的好友 |
| --- | --- |
| `groups.owner_id` 指向的真实群主 | 可以 |
| `role=1` 管理员 | 可以 |
| `role=0` 普通成员 | 不可以，返回 `1301` |
| 非成员 | 不可以，返回 `1301` |
| 不是 `owner_id`、但脏数据中意外为 `role=2` | 不可以 |

“真实群主”必须看 `groups.owner_id`。不能看到任意 `role=2` 就授权，否则异常数据中的第二个“群主”会得到真实管理权。

被邀请人还必须满足：

1. 用户真实存在；
2. 是当前操作者的好友；
3. 还不是该群成员；
4. 群当前人数小于 `max_members`。

这里只实现“邀请自己的好友”。管理员不能因为群主与某人是好友，就代替群主邀请一个自己不认识的人。

### 2.2 移除权限

| 操作者 | 目标是群主 | 目标是管理员 | 目标是普通成员 |
| --- | --- | --- | --- |
| 真实群主 | 禁止 `1305` | 可以 | 可以 |
| 管理员 | 禁止 `1305` | 禁止 `1309` | 可以 |
| 普通成员/非成员 | 禁止 `1301` | 禁止 `1301` | 禁止 `1301` |

群主不能通过“移除自己”来退群，因为那会留下没有群主的群。正确流程是先转让群主，再退群，这属于 `GROUP-004`。

管理员也不能用移除接口移除自己。管理员自愿退群同样应走后续的退群业务。

### 2.3 查看权限

任何当前群成员都能查看成员列表；非成员返回 `5001`。这避免登录用户通过猜群 ID 枚举陌生群的成员资料。

## 3. 为什么“先查人数，再 INSERT”仍会超员

假设群上限是 2，目前只有群主，还剩 1 个位置。两个管理员同时邀请不同好友：

```text
请求 A：COUNT(*) = 1，判断还能加
请求 B：COUNT(*) = 1，判断还能加
请求 A：INSERT 好友甲
请求 B：INSERT 好友乙
最终人数 = 3，超过上限
```

问题不在 COUNT 写错了，而在两个请求同时观察了旧状态。

解决办法是：所有成员变化都先锁同一条群记录。

```sql
SELECT ... FROM `groups` WHERE id = ? FOR UPDATE;
```

同一个群的两个邀请事务不能同时持有这把行锁。请求 B 必须等待 A 提交，随后重新 COUNT，看到人数已经是 2，于是返回 `1304`。不同群锁的是不同记录，仍然可以并行处理。

可以把群行锁想成“该群成员名册的唯一编辑笔”：读者可以很多，但同一时间只有一个事务拿笔修改名册。

## 4. 添加成员的完整事务

实现入口在 [group.go](../backend/internal/service/group.go)，事务与 SQL 在 [group_repository.go](../backend/internal/repository/group_repository.go)。添加顺序是：

```text
BEGIN
  1. 锁 groups 行，并确认群存在
  2. 锁操作者成员行，检查群主/管理员权限
  3. 锁目标成员行，确认尚未入群
  4. 按用户 ID 升序锁操作者和目标 users 行
  5. 检查两人仍是好友
  6. COUNT 当前成员并检查 max_members
  7. INSERT group_members(role=0)
  8. INSERT cache_reconcile_events(group_members, groupID)
COMMIT
  9. 尽力立即重建该群 Redis 投影
```

### 4.1 为什么还要锁 users 行

好友删除也会按用户 ID 升序锁住这两条用户记录。邀请和删除好友因此不能在检查关系的关键时刻交叉执行：

- 邀请先拿到用户锁：它在锁内确认好友并完成入群；好友删除随后发生。
- 删除好友先拿到用户锁：邀请随后检查时会看到好友关系已经不存在。

固定按较小 ID、较大 ID 的顺序加锁，是为了降低两个事务反向等待造成死锁的风险。

### 4.2 为什么数据库唯一索引仍然必要

Service 已经检查目标是否在群里，但数据库唯一索引仍是最后一道防线。即使未来某段代码忘了检查，数据库也不会插入重复关系。

若 INSERT 遇到唯一键冲突，Repository 将 MySQL 1062 映射为 `repository.ErrConflict`，Service 再映射为业务码 `1303`。客户端得到的是“已经是群成员”，而不是模糊的 500。

### 4.3 为什么协调事件必须在同一事务

成员 INSERT 和 `cache_reconcile_events` 一起提交：

```text
成员写成功 + 事件写失败 => 整个事务回滚
成员写成功 + 事件写成功 => 一定留下后台修复凭据
```

如果事件在事务外写，进程可能恰好在两次写之间崩溃，MySQL 已经有成员，却永远没有人负责修 Redis。

## 5. 移除成员的完整事务

移除沿用同一条锁顺序：

```text
BEGIN
  1. 锁 groups 行，并确认群存在
  2. 锁操作者成员行，检查管理权限
  3. 锁目标成员行，确认目标仍在群内
  4. 根据真实 owner_id 和目标 role 检查权限矩阵
  5. DELETE group_members
  6. INSERT cache_reconcile_events(group_members, groupID)
COMMIT
  7. 尽力立即重建该群 Redis 投影
```

先锁群、再锁成员的顺序与资料更新和邀请保持一致。一个项目里如果有的代码“先锁群再锁成员”，另一些代码反过来，两个事务更容易互相等待。

目标已经被另一个请求移除时返回 `1310`。前端会刷新成员 Query，以当前服务器状态自愈，而不是把陈旧页面继续当真。

## 6. MySQL 与 Redis 各自负责什么

本项目坚持：

```text
MySQL = 权威事实
Redis = 可丢失、可重建的快速投影
```

群成员在 Redis 有三份主要投影：

```text
group_members:{gid}       Set   群 -> 成员
group_member_info:{gid}   Hash  成员 -> role / muted_until
user_groups:{uid}         Set   用户 -> 群
```

前两个是正向关系，第三个是反向关系，所以验收清单称它们为“双向 Redis Set”。实际重建还维护 owner index 和 loaded marker。

### 6.1 为什么不用 Redis 的旧增量 Add/Remove

看起来最直接的方式是：MySQL INSERT 后执行一个 Redis `SADD`，DELETE 后执行 `SREM`。但请求可能延迟或乱序：

```text
先移除用户
后来用户重新入群
旧的 SREM 最后才到达 Redis
=> Redis 错误地把新成员删掉
```

因此生产路径不使用旧的增量 `AddGroupMember/RemoveGroupMember`。它只调用 `CacheTruthService.ReconcileGroupMembers`：重新读取 MySQL 的“当前完整真相”，再用 Lua 原子替换 Set、Hash 和反向 Set。旧事件晚到也只会再次读取新状态，不会复活过去。

### 6.2 Redis 失败为什么不返回业务失败

执行顺序是先 COMMIT MySQL，再做 Redis fast path。假如 Redis 恰好不可用：

```text
MySQL 已经添加成员
协调事件也已提交
立即刷新 Redis 失败
HTTP 仍返回成功
后台 Worker 稍后读取事件并重建
```

这是正确且诚实的结果。若此时返回“添加失败”，客户端重试可能只得到 `1303`，用户会看到“第一次说失败、第二次又说已经存在”的矛盾体验。

`context.WithoutCancel` 还保证：客户端刚好断开连接，不会取消已经提交事务后的缓存修复尝试。

## 7. 分页是怎样工作的

接口约束：

- `limit` 默认 20；
- `limit` 必须是 1～100；
- `offset` 默认 0，不能为负数；
- `items` 即使为空也返回 `[]`，不返回 `null`。

示例：

```http
GET /api/v1/group/100/members?limit=2&offset=0
```

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 501,
        "group_id": 100,
        "user_id": 7,
        "role": 2,
        "username": "alice",
        "avatar_url": "/uploads/avatars/a.png",
        "joined_at": "2026-08-31T10:00:00Z"
      }
    ],
    "pagination": {
      "total": 1,
      "offset": 0,
      "limit": 2,
      "has_more": false
    }
  }
}
```

`total` 来自 `COUNT(*)`，不是当前页的 `items.length`。假设群里有 230 人，`limit=100` 的第一页只有 100 项，但总人数必须显示 230。

成员列表始终查询 MySQL。不能拿 Redis Set 当列表，因为 Set 没有稳定顺序，也没有 username/avatar；更重要的是 Redis 在协调事件重试期间可能短暂落后于 MySQL。

## 8. 前端为什么每次取 100，再收齐全部页

群上限目前是 500，聊天页要用完整成员资料显示发送者头像，管理页也要用完整列表：

- 判断当前用户是不是管理员；
- 过滤已经入群的好友，避免重复邀请；
- 显示真实总人数；
- 正确显示第二页以后的成员。

共享帮助函数 [groupMembers.ts](../frontend/src/features/groups/groupMembers.ts) 每次合法请求 100 条，根据服务端返回的 `offset + items.length` 继续下一页，直到 `has_more=false`。聊天页和管理抽屉使用同一个 Query Key 和同一个查询函数，避免同一缓存键一会儿存 100 条、一会儿又尝试请求非法的 500 条。

前端权限也与后端保持一致：

- `group.owner_id === currentUserId` 才是真群主；
- `role=1` 是管理员；
- 群主能移除管理员和普通成员；
- 管理员只能移除普通成员；
- 不能在这里移除自己或真实群主。

前端隐藏按钮只是改善体验，不是安全边界。攻击者可以绕过页面直接发 HTTP，所以后端必须再次完整检查权限。

## 9. 三层代码各做什么

### Handler：只处理 HTTP

[group_handler.go](../backend/internal/api/group_handler.go) 负责：

1. 从 JWT Context 读取当前用户 ID；
2. 校验 `groupID/memberID` 是正整数；
3. 绑定 `{member_id}` 或读取分页参数；
4. 调用 `GroupCoreService`；
5. 输出统一响应信封。

Handler 不直接 COUNT、不查好友，也不写 Redis。

### Service：决定业务顺序

[group.go](../backend/internal/service/group.go) 负责权限表、事务步骤、错误码转换，以及提交后的缓存刷新。这里是“业务真正发生的地方”。

### Repository：实现持久化细节

[group_repository.go](../backend/internal/repository/group_repository.go) 负责事务绑定、`FOR UPDATE`、COUNT、JOIN 分页和成员 DELETE。Service 只依赖窄接口，因此单元测试能用内存 fake，不需要每次都启动 MySQL。

已完成的资料、成员与 GROUP-003 角色/禁言能力组合成 `GroupCoreService`；尚未完成的转让与退群仍留在更大的 `GroupService` 契约中。这是接口隔离：Router 只要求当前真实可用的能力。

## 10. 业务错误码速查

| code | HTTP | 含义 |
| --- | --- | --- |
| `1002` | 400 | ID、JSON 或分页参数非法 |
| `1104` | 404 | 被邀请用户不存在 |
| `1301` | 403 | 不是群主或管理员 |
| `1302` | 404 | 群不存在 |
| `1303` | 409 | 目标已经在群内 |
| `1304` | 409 | 群已满 |
| `1305` | 403 | 不能移除真实群主 |
| `1308` | 403 | 只能邀请自己的好友 |
| `1309` | 403 | 管理员不能移除管理员同级 |
| `1310` | 404 | 目标成员已不存在 |
| `5001` | 403 | 查看者不是群成员 |

前端优先根据 code 映射中文提示，而不是依赖后端英文 message。code 是稳定契约，message 将来可以调整或国际化。

## 11. 自己动手验证

准备两个已经互为好友的账号。先用群主 token：

```powershell
$base = 'http://localhost:18080/api/v1'
$ownerToken = '群主的 access_token'
$ownerHeaders = @{ Authorization = "Bearer $ownerToken" }
$groupID = 100
$friendID = 8
```

邀请好友：

```powershell
Invoke-RestMethod -Method Post -Uri "$base/group/$groupID/member" `
  -Headers $ownerHeaders -ContentType 'application/json' `
  -Body (@{ member_id = $friendID } | ConvertTo-Json)
```

分页查看：

```powershell
Invoke-RestMethod -Method Get `
  -Uri "$base/group/$groupID/members?limit=20&offset=0" `
  -Headers $ownerHeaders
```

移除成员：

```powershell
Invoke-RestMethod -Method Delete `
  -Uri "$base/group/$groupID/member/$friendID" `
  -Headers $ownerHeaders
```

在 MySQL 检查权威关系和协调事件：

```sql
SELECT group_id, user_id, role, muted_until, joined_at
FROM group_members
WHERE group_id = 100
ORDER BY role DESC, joined_at, id;

SELECT resource_type, resource_id, status, attempts, last_error
FROM cache_reconcile_events
WHERE resource_type = 'group_members' AND resource_id = 100
ORDER BY id DESC;
```

在 Redis CLI 检查正向和反向投影：

```text
SMEMBERS group_members:100
HGETALL group_member_info:100
SMEMBERS user_groups:8
GET group_member_loaded:100
```

添加后，成员 ID 应同时出现在 `group_members:100` 和该用户的 `user_groups:8`；移除后应同时消失。`group_member_info:100` 里也不应留下被移除成员的孤儿字段。

## 12. 测试分别证明什么

- [group_test.go](../backend/internal/service/group_test.go)：用内存 fake 证明好友限制、容量、权限矩阵、重复成员、回滚、分页与缓存失败语义。
- [group_handler_test.go](../backend/internal/api/group_handler_test.go)：用真实 Gin Router 和 JWT 证明 JSON、路径参数、分页响应、HTTP 状态码与路由不再返回 501。
- [group_members_integration_test.go](../backend/internal/service/group_members_integration_test.go)：用真实 MySQL/Redis 证明锁、SQL、正反向 Set、角色 Hash 与并发容量。
- 前端成员测试：证明多页收集、权限按钮、总人数、错误提示和防重复提交。

日常快速测试：

```powershell
Set-Location 'E:\IT\my_IM\backend'
go test ./...

Set-Location 'E:\IT\my_IM\frontend'
npm.cmd run typecheck
npm.cmd test
```

真实依赖集成测试需要 Docker Compose 中的 MySQL 和 Redis 已启动：

```powershell
Set-Location 'E:\IT\my_IM\backend'
$env:MYIM_INTEGRATION = '1'
go test ./internal/service -run 'GroupMember.*Integration' -count=1 -v
```

集成测试会创建带随机后缀的用户和群，并按精确 ID 清理，不会清空整个数据库或 Redis。

## 13. 建议的阅读顺序

如果你是第一次读后端项目，可以按这个顺序：

1. 看 [group_handler.go](../backend/internal/api/group_handler.go)，理解请求怎样进入程序；
2. 看 [group.go](../backend/internal/service/group.go) 的 `AddMember`，对照本文第 4 节逐行走一遍；
3. 看 [group_repository.go](../backend/internal/repository/group_repository.go)，把接口方法和真实 SQL 对上；
4. 看 `RemoveMember`，观察它怎样复用同一套事务骨架；
5. 看 `ListMembers` 和分页 SQL；
6. 最后看测试，尝试把每个测试名翻译成一句业务承诺。

当你能回答下面三个问题，就真正理解了这一阶段：

1. 为什么容量检查必须发生在群行锁里面？
2. 为什么 Redis 刷新失败不能把已提交的邀请返回成失败？
3. 为什么前端隐藏“移除”按钮后，后端仍然必须做权限检查？

答案分别是：避免并发超员；MySQL 才是已提交的事实且已有可重试事件；客户端永远不能充当安全边界。
