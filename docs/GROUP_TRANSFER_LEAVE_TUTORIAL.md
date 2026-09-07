# 群聊第四步教程：转让群主与退群

这篇教程对应开发清单的 `GROUP-004`。本阶段完成两个必须一起理解的业务：群主把群交给另一位现有成员，以及普通成员或管理员主动退出群聊。

已经完成的接口是：

| 功能 | 方法与路径 | 请求体 | 成功响应 |
| --- | --- | --- | --- |
| 转让群主 | `PUT /api/v1/group/:groupID/owner` | `{ "new_owner_id": 9 }` | `{ "code": 0, "message": "ok" }` |
| 退出群聊 | `POST /api/v1/group/:groupID/leave` | 无 | `{ "code": 0, "message": "ok" }` |

两个接口都必须携带登录后的 access token。成员 ID 永远来自服务端已有数据，前端不能自己猜一个 ID 来“创建”新群主。

## 1. 先记住本阶段的两个核心规则

第一条：一个正常群在任何时刻都只能有一位真实群主。

```text
groups.owner_id = 7
group_members(group_id=100, user_id=7).role = 2
```

第二条：真实群主不能直接退群，必须先把群主转给另一位成员。

```text
群主想退群
  -> 先选择一位现有成员并转让
  -> 自己变成普通成员
  -> 再调用退群接口
```

如果允许群主直接删除自己的成员行，`groups.owner_id` 会继续指向一个已经不在群里的人，其他成员也没有合法的人可以继续管理群。这就是为什么后端必须返回 `1306`，而不是悄悄帮群主退出。

## 2. “真实群主”为什么看 `owner_id`

这个项目用两个位置表达群主身份：

- `groups.owner_id` 是唯一的权威群主 ID；
- `group_members.role=2` 用于成员列表展示和缓存投影。

正常数据中两者应该一致。但数据库被人工修改、旧版本发生故障或代码存在缺陷时，可能意外多出一条 `role=2`。因此所有高权限判断都必须以 `groups.owner_id` 为准，不能看到 `role=2` 就直接放行。

可以把它理解为学校档案：`owner_id` 是校务系统里的正式任命，`role=2` 是胸牌。胸牌印错了不会让一个人真的成为校长。

## 3. 转让到底要同时修改什么

假设群 100 的原群主是 7，准备转给成员 9：

| 数据 | 转让前 | 转让后 |
| --- | --- | --- |
| `groups.owner_id` | `7` | `9` |
| 用户 7 的成员角色 | `role=2` | `role=0` |
| 用户 9 的成员角色 | `role=0/1` | `role=2` |

这就是任务清单所说的“三处状态”。旧群主统一降为普通成员，而不是管理员；这样转让不会偷偷额外授予一份管理员权限。

还有一个很隐蔽但重要的状态：新群主原来可能正在被禁言。真实群主不能成为禁言对象，也没有更高权限的人替他解禁，所以转让时会把新群主的 `muted_until` 一并清空。否则可能造出一个长期无法发言、也无法被任何人解除禁言的群主。

## 4. 为什么三处更新必须放在一个事务里

错误做法是依次执行三个独立 SQL：

```text
更新 owner_id 成功
旧群主降级成功
新群主升级时数据库断开
```

这时数据库会留下互相矛盾的状态。事务把这些写操作包装成一个不可分割的整体：

```text
BEGIN
  1. 锁定 groups 行
  2. 锁定旧群主成员行
  3. 锁定新群主成员行
  4. 再检查权限和成员身份
  5. 旧群主 role = 0
  6. 新群主 role = 2，muted_until = NULL
  7. groups.owner_id = 新群主 ID
  8. 写入 group_members 缓存协调事件
COMMIT
```

其中任何一步报错，Repository 都会执行 `ROLLBACK`。因此外部只能看到完整的旧状态或完整的新状态，不会看到“转了一半”。

协调事件也在同一事务内。如果 MySQL 状态成功了，就一定留下 Redis 可重试修复的凭据；如果事件写入失败，前面的群主修改也一起回滚。

## 5. 为什么先锁群行，再锁成员行

转让、退群、添加、移除、任免和禁言都会先锁同一个 `groups` 行，再锁相关成员行：

```text
group -> operator member -> target member
```

锁可以理解为取号办理。两个并发请求都想修改同一个群时，只有先拿到群行锁的请求能继续，另一个必须等待。第一个事务提交后，等待者会读取最新数据并重新检查权限。

例如原群主同时发出两次转让请求：

```text
请求 A：转给 9，先拿到群锁并成功提交
请求 B：转给 10，随后拿到锁，再读到 owner_id 已经是 9
请求 B：发现操作者 7 已不是真实群主，返回 1301
```

这样不会产生两个群主。统一锁顺序还会降低事务互相反向等待造成死锁的概率。

注意：权限必须在拿到锁并读取最新数据后再次判断。只在事务外先查一次，会出现“检查时有权限，真正写入时权限已经转走”的竞态。

## 6. 转让群主的完整业务校验

Service 会依次保证：

1. `groupID`、当前用户 ID 和 `new_owner_id` 都是正整数；
2. 不能把群主转让给自己；
3. 群必须存在；
4. 操作者必须有成员行，并且必须等于最新的 `groups.owner_id`；
5. 新群主必须已经在当前群里；
6. 三处核心状态、解除新群主禁言和缓存协调事件一起提交。

为什么目标必须是“现有成员”？因为直接把陌生用户写成 owner 会绕过好友邀请、群容量和用户存在性等规则。正确流程是先通过 GROUP-002 把他加入群，再转让。

## 7. 退群为什么也使用事务

普通成员或管理员退群的事务是：

```text
BEGIN
  1. 锁定 groups 行并确认群存在
  2. 锁定自己的 group_members 行
  3. 确认自己确实是成员
  4. 若 groups.owner_id == 当前用户，返回 1306 并回滚
  5. 删除自己的成员行
  6. 写入 group_members 缓存协调事件
COMMIT
```

管理员不需要先取消管理员身份，退群会直接删除整条成员记录。普通成员和管理员调用同一个接口，用户 ID 只能从 JWT 取得；请求体不能指定“让谁退群”，所以不能伪造别人的 ID 把别人踢出去。

后端对非成员重复调用退群仍返回 `5001`，如实说明“已经没有这条成员关系”。前端把它当作删除类命令的目标状态已经达到：清理陈旧会话和 Query，再同步权威群列表。这样既保留清楚的服务端契约，也能正确处理“第一次退群成功、但成功响应在网络中丢失”后的重试。

## 8. MySQL、Redis 与 WebSocket 各自负责什么

三者不是三个平级真相：

```text
MySQL：权威账本
  -> cache_reconcile_events：可靠的待修复凭据
  -> Redis：可丢失、可重建的快速投影
  -> WebSocket：在线界面的即时刷新提示
```

事务提交后，Service 会尽力立即执行 `ReconcileGroupMembers`，从 MySQL 读取完整快照并重建：

- `group_members:{gid}`：群到成员的 Set；
- `group_member_info:{gid}`：成员角色与禁言信息 Hash；
- `user_groups:{uid}`：用户到群的反向 Set；
- loaded marker 和内部 owner index。

转让不会改变成员集合，但会改变 Hash 中两人的角色并清除新群主的禁言。退群会同时从群成员 Set、成员 Hash 和退出者的 `user_groups` 中移除关系。

如果 Redis 暂时不可用，已经提交的 HTTP 请求仍然成功，因为不能把真实存在的 MySQL 成功说成失败。后台 Reconciler 会根据协调事件重试，并总是重新读取当前 MySQL 真相，不会重放一个可能已经过期的旧增量命令。

## 9. WebSocket 通知为什么只是“刷新提示”

本阶段使用三种群变更消息：

| `type` | `data` | 用途 |
| --- | --- | --- |
| `groupUpdated` | `{ groupId, reason: "owner_transferred" }` | 群主发生变化，刷新群资料和成员角色 |
| `groupUpdated` | `{ groupId, reason: "member_left" }` | 有成员退出，刷新成员列表 |
| `groupRemoved` | `{ groupId, reason: "left" }` | 退出者从本地移除群会话 |

通知必须在 MySQL 提交之后发送。否则客户端可能先收到通知去查询，却只能查到事务提交前的旧状态。

WebSocket 不是可靠数据库：用户可能离线、连接可能刚好断开、当前 Hub 也只持有本应用实例中的连接。因此通知失败不能回滚已经提交的业务。客户端收到 `groupUpdated` 后只做 Query 失效并重新请求 HTTP；收到 `groupRemoved` 时还会移除退出者的本地会话。没有收到事件时，也能在重连或下次打开页面时从 MySQL 权威接口恢复。

当前项目仍是单应用实例部署。以后做横向扩容时，需要使用 Redis Pub/Sub、RabbitMQ 或专用事件总线，把同一事件扇出到其他实例上的 Hub；不能把 Redis 在线状态键误当成跨节点消息总线。

## 10. Handler、Service、Repository 的分工

### Handler

[group_handler.go](../backend/internal/api/group_handler.go) 负责：

- 从 JWT Context 读取当前用户 ID；
- 解析并校验路径里的 `groupID`；
- 绑定转让请求中的 `new_owner_id`；
- 调用 Service；
- 返回统一 `{code,message,data}` 信封。

### Service

[group.go](../backend/internal/service/group.go) 负责：

- 权限和目标成员规则；
- 固定锁顺序与事务编排；
- 把底层错误映射为稳定业务码；
- 提交后触发缓存刷新和 WS 提示。

### Repository

[group_repository.go](../backend/internal/repository/group_repository.go) 负责：

- 把回调中的 SQL 绑定到同一个 `sql.Tx`；
- 执行 `SELECT ... FOR UPDATE`；
- 只更新明确字段，避免资料更新意外覆盖 owner；
- 写缓存协调事件。

把权限只写在前端按钮或 Handler 都不安全。攻击者可以手写 HTTP 请求，未来的后台任务也可能绕过 Handler；业务不变量必须放在所有入口都会调用的 Service。

## 11. 前端是怎样接上的

群管理抽屉会根据 `group.owner_id` 判断当前用户是否为真实群主：

- 只有真实群主能看到其他成员的“转让群主”操作；
- 点击后出现包含目标用户名的二次确认，避免点错；
- 转让成功后，本地 Query 会一次更新 `owner_id`、旧群主 role 和新群主 role，再失效查询向服务端复核；
- 普通成员和管理员能看到“退出群聊”；
- 真实群主点击退出时会得到明确提示，要求先转让；
- 退群成功后清理群资料/成员 Query，并从会话列表中移除该群。
- 退群重试若得到 `5001`（成员关系已不存在）或 `1302`（群已不存在），也会按目标状态已达到来清理本地幽灵会话。

前端立即更新是为了操作手感，随后失效查询是为了最终以服务端真相校正。后端的权限检查仍然是安全边界。

## 12. 错误码速查

| code | HTTP | 场景 |
| --- | --- | --- |
| `1002` | 400 | ID 非法、请求体缺字段或把群主转给自己 |
| `1301` | 403 | 操作者不是真实群主，不能转让 |
| `1302` | 404 | 群不存在 |
| `1306` | 409 | 当前用户是真实群主，不能直接退群 |
| `1310` | 404 | 新群主不是当前群成员 |
| `5001` | 403 | 退群者已经不是群成员 |

客户端应优先按 `code` 处理，不要依赖英文 `message` 文本。

## 13. 用 PowerShell 自己调用

先准备 ID 和 token：

```powershell
$base = 'http://localhost:18080/api/v1'
$groupID = 100
$newOwnerID = 9
$ownerToken = '替换为原群主 access_token'
$oldOwnerHeaders = @{ Authorization = "Bearer $ownerToken" }
```

转让群主：

```powershell
$body = @{ new_owner_id = $newOwnerID } | ConvertTo-Json
Invoke-RestMethod -Method Put `
  -Uri "$base/group/$groupID/owner" `
  -Headers $oldOwnerHeaders -ContentType 'application/json' -Body $body
```

原群主现在已经是普通成员，可以退群：

```powershell
Invoke-RestMethod -Method Post `
  -Uri "$base/group/$groupID/leave" `
  -Headers $oldOwnerHeaders
```

如果在转让前直接执行第二条命令，应得到 HTTP 409 和 `code=1306`。这不是系统故障，而是业务规则正常生效。

## 14. 如何检查三层状态

先查看 MySQL：

```sql
SELECT id, owner_id, name FROM `groups` WHERE id = 100;

SELECT group_id, user_id, role, muted_until
FROM group_members
WHERE group_id = 100
ORDER BY user_id;

SELECT resource_type, resource_id, status, attempts, last_error
FROM cache_reconcile_events
WHERE resource_type = 'group_members' AND resource_id = 100
ORDER BY id DESC;
```

再查看 Redis：

```text
SMEMBERS group_members:100
HGETALL group_member_info:100
SMEMBERS user_groups:7
SMEMBERS user_groups:9
GET group_member_loaded:100
```

转让后应看到 owner_id 指向 9、用户 7 的 role 是 0、用户 9 的 role 是 2 且没有 `muted_until`。用户 7 退群后，所有与 `group=100,user=7` 相关的正反向 Redis 投影都应消失。

## 15. 测试在证明什么

- Service 单元测试：只有真实群主可转让、目标必须在群内、自己转自己失败、群主不能退出、管理员/成员可退出、事务失败会回滚。
- Repository 测试：更新 owner SQL 不覆盖群名公告，成员角色/禁言更新和协调事件都使用同一事务。
- MySQL + Redis 集成测试：转让后的三处状态与完整 Hash 一致；退群后 Set、Hash、`user_groups` 三向清理；故意让 Redis 快速刷新失败后 Reconciler 能修复；两个并发转让只有一个提交成功。
- Handler/Router 测试：JWT 用户 ID 正确透传、缺失 `new_owner_id` 返回 1002、两条 GROUP-004 路由不再返回 501。
- WebSocket 契约测试：camelCase 字段和事件名与 TypeScript 联合类型完全一致，发送失败不改变已提交状态。
- 前端测试：二次确认、权限按钮、成功后的 Query/会话更新、1306 等中文错误以及防重复点击。

验证命令：

```powershell
Set-Location 'E:\IT\my_IM\backend'
go test ./...
go vet ./...

$env:MYIM_INTEGRATION = '1'
go test ./internal/service -run '^TestGroupLifecycleDockerIntegration$' -count=1 -v

Set-Location 'E:\IT\my_IM\frontend'
npm.cmd run typecheck
npm.cmd test -- --run
npm.cmd run build
```

没有启动测试用 MySQL/Redis 时，集成测试会明确跳过；这与测试失败不是一回事。

## 16. 可以自己做的故障实验

为了真正理解事务，可以在测试 fake 或本地测试库中让“更新新群主角色”或“写协调事件”返回错误，然后检查：

```text
groups.owner_id 没变
旧群主 role 没变
新群主 role/muted_until 没变
没有半条协调事件
没有发送成功 WS 通知
```

再模拟 Redis 刷新失败，结果应该不同：MySQL 已经完整提交，HTTP 仍成功，协调事件保留为待重试，之后运行 Reconciler 会把 Redis 修正。这两个实验能帮你区分“事务内的权威写失败”和“提交后的缓存加速失败”。

## 17. 最后用四句话记住

1. 真正的群主只看 `groups.owner_id`，成员角色是需要同步维护的投影。
2. 转让的 owner_id、旧角色、新角色和协调事件必须同事务成功或同事务失败。
3. 群主先转让再退群；管理员和普通成员可以直接删除自己的成员关系。
4. MySQL 是真相，Redis 可重建，WebSocket 只负责让在线界面更快知道“该刷新了”。
