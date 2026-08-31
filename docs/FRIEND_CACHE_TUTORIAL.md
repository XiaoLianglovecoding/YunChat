# 好友与缓存真相模块小白教程

这篇教程对应 `DEVELOPMENT_TASKS.md` 第 3 章：`FRIEND-001` 到 `FRIEND-005`、`CACHE-001`。

你会看到的不只是“接口怎么写”，而是一个后端最重要的思维方式：

> MySQL 保存不能丢的事实；Redis 保存可以删除后重建的投影；WebSocket 只负责及时提醒。

如果先记住这一句话，后面的事务、Outbox、loaded marker 和重连刷新就都有了共同主线。

## 1. 本阶段完成了什么

现在可以真实使用这些接口：

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| POST | `/api/v1/friend/request` | 发送好友申请 |
| GET | `/api/v1/friend/requests` | 分页查询收到的待处理申请 |
| POST | `/api/v1/friend/accept` | 接受申请 |
| POST | `/api/v1/friend/reject` | 拒绝申请 |
| GET | `/api/v1/friend/list` | 分页查询好友、资料、在线态和拉黑态 |
| DELETE | `/api/v1/friend/:friendID` | 双向删除好友 |
| POST | `/api/v1/friend/block` | 拉黑用户 |
| POST | `/api/v1/friend/unblock` | 解除拉黑 |

同时实现了：

- 同向和反向重复申请检查。
- 接受申请与双向好友写入的 MySQL 事务。
- 重复接受、重复拒绝、重复删除、重复解除拉黑的幂等语义。
- 好友、黑名单、群成员 Redis 投影的启动预热和按需回源。
- MySQL 事务协调事件与后台重试修复。
- owner 级 Redis 锁与 MySQL 行锁，防止旧快照晚写覆盖新状态。
- 缓存全量重建和一致性巡检命令。
- WebSocket JWT 握手、单用户连接替换、心跳租约及好友事件。
- Go/TypeScript 事件契约和前端 Query 刷新。

## 2. 先认识代码地图

建议按下面顺序阅读，不要一上来钻进所有 SQL：

| 文件 | 先看什么 |
| --- | --- |
| `backend/internal/api/friend_handler.go` | HTTP 参数如何变成 Service 调用 |
| `backend/internal/service/friend.go` | 业务规则和事务编排 |
| `backend/internal/repository/friend_repository.go` | 事务内真正执行的 SQL |
| `backend/internal/service/cache_truth.go` | 缓存回源、重建、巡检和 Worker |
| `backend/internal/repository/cache_redis.go` | loaded marker、锁和原子替换 |
| `backend/internal/repository/cache_mysql.go` | MySQL 真相查询与协调事件领取 |
| `backend/internal/redis/lua_private_msg.go` | 私聊最终的 Redis 原子校验 |
| `backend/internal/ws/hub.go` | WebSocket 连接和在线租约 |
| `frontend/src/components/realtime/RealtimeBootstrap.tsx` | 前端收到事件后如何刷新 |
| `backend/cmd/cachectl/main.go` | 运维重建/巡检入口 |

调用方向始终是：

```text
HTTP Handler
    ↓
FriendService（规则、事务编排、错误码）
    ↓
FriendRepository（MySQL）
    ↓ 提交后
Redis fast path + WebSocket 提示
```

Handler 不写 SQL，Repository 不决定“能不能加自己”，这就是分层。

## 3. 三张关系表分别表达什么

### 3.1 `friend_requests`：申请过程

一行表示一个有方向的申请：

```text
from_user_id = 1
to_user_id   = 2
status       = 0
```

状态约定：

- `0`：pending，待处理。
- `1`：accepted，已接受。
- `2`：rejected，已拒绝或因拉黑而终结。

表上有 `(from_user_id, to_user_id)` 唯一键，所以同方向同一时刻只会有一条申请。正在等待的申请不能重复发送；如果旧申请已经是 accepted/rejected 终态，重新发送会在同一事务中删除旧行，再插入一条具有新 ID 的 pending 申请。不复用旧 ID 是为了防止过期的“接受申请”重试误操作新一轮申请。

### 3.2 `friendships`：已经成立的好友事实

一段好友关系保存两行：

```text
user_id=1, friend_id=2
user_id=2, friend_id=1
```

为什么不只存一行 `(smaller_id, larger_id)`？双向两行让“查询我的好友”始终是简单的：

```sql
SELECT friend_id FROM friendships WHERE user_id = ?;
```

代价是写入和删除必须保证两行一起成功，所以必须使用事务。

### 3.3 `blacklist`：谁主动拉黑了谁

这也是有方向的：

```text
user_id=1, blocked_id=2
```

本项目冻结的产品规则是：

- 拉黑不删除好友关系。
- 拉黑会拒绝双方旧的 pending 申请。
- 任意一方拉黑，双方都不能私聊。
- 解除拉黑不会自动恢复已经拒绝的申请。

选择“保留好友”的原因很实际：当前前端没有独立黑名单页面。如果拉黑时直接删好友，联系人会消失，用户反而没有入口解除拉黑。好友列表的 `is_blocked` 字段也正好支持原地显示和解除。

## 4. 为什么发送申请也要事务和锁

### 4.1 只靠唯一键挡不住反向申请

数据库唯一键是有方向的：

```text
(A, B) 与 (B, A) 不相同
```

假设 A 和 B 同时发送：

```text
请求 1：检查没有 pending A→B
请求 2：检查没有 pending B→A
请求 1：插入 A→B
请求 2：插入 B→A
```

两个插入都不违反唯一键，最后就出现两条互相交叉的 pending。

### 4.2 解决方法：所有人按相同顺序锁用户

`LockFriendUsers` 总是先锁较小 ID，再锁较大 ID：

```sql
SELECT id FROM users WHERE id = ? FOR UPDATE;
SELECT id FROM users WHERE id = ? FOR UPDATE;
```

无论请求是 A→B 还是 B→A，锁顺序都相同。例如 A=10、B=20，两个事务都先锁 10，再锁 20。

结果是：

```text
事务 1 拿到 10、20 的锁
事务 2 等待
事务 1 检查并插入申请，然后提交
事务 2 获得锁，重新检查，看到反向 pending，返回 1204
```

“先查再写”本身并不安全；“在同一个事务、同一组锁里先查再写”才安全。

### 4.3 发送申请的完整规则

事务内按顺序执行：

```text
检查不能加自己
    ↓
按 ID 顺序锁两名用户，并确认目标存在
    ↓
检查任一方向黑名单
    ↓
检查任一方向是否已有好友行
    ↓
检查任一方向是否已有 pending
    ↓
删除同方向终态旧申请，再插入一条新 ID 的申请
    ↓
提交
```

对应错误码：

| 场景 | 业务码 |
| --- | --- |
| 自己加自己 | 1201 |
| 已经是好友 | 1202 |
| 任一方向拉黑 | 1203 |
| 同向或反向申请待处理 | 1204 |
| 目标用户不存在 | 1104 |

申请附言会去掉首尾空白，最多 200 个 Unicode 字符，并拒绝控制字符。这里按“字符”而不是 UTF-8 字节计算，所以 200 个汉字不会被误判成 600 个字符。

## 5. 接受申请：一个事务里完成三件事

接受不是简单的 `UPDATE status=1`。正确的事务边界是：

```text
锁两名用户
    ↓
SELECT friend_requests ... FOR UPDATE
    ↓
确认操作者就是 to_user_id
    ↓
再次检查双方黑名单
    ↓
pending -> accepted
    ↓
INSERT IGNORE 两个方向的 friendships
    ↓
为双方插入 cache_reconcile_events
    ↓
COMMIT
```

如果第二条好友行写入失败，事务会回滚，申请不会提前变成 accepted。这样数据库不会出现“申请显示已接受，但只有单向好友”的半成品。

### 5.1 为什么要在事务里重新读申请

Service 在事务前读一次申请，是为了知道需要锁哪两个用户；真正决定状态时，会在事务里再次执行 `FOR UPDATE`。

第一次读取只能叫“预览”，不能当最终依据。因为两次读取之间，另一个请求可能已经接受、拒绝或拉黑。

### 5.2 幂等是什么意思

幂等可以理解为：同一个请求因为超时被客户端重试，不会把数据做坏。

接受接口的规则：

- pending：更新状态并补齐双向好友。
- accepted：仍返回成功，并用 `INSERT IGNORE` 检查/补齐两行。
- rejected：返回 1205，终态拒绝不能偷偷变成接受。

20 个并发接受请求最终也只有两条 `friendships`，因为唯一键和事务共同兜底。

拒绝也有明确语义：

- pending：改为 rejected。
- rejected：重复调用成功。
- accepted：不能再伪装成拒绝成功，返回 1205。

## 6. 删除、拉黑与分页列表

### 6.1 删除好友

删除在事务内执行双向 `DELETE`，同时把双方以前的 accepted 申请置为 rejected，并为双方记录缓存协调事件。这样延迟到达的旧 accept 重试不会把已删除的好友关系“复活”。好友本来就不存在时，删除仍成功，方便客户端安全重试。

事务提交后立即触发双方 owner 的 fast-path reconciliation。它会重新读取 MySQL 当前真相、完整替换双方各自拥有的投影，最终移除：

```text
friend:A:B
friend:B:A
```

如果此时 Redis 故障，MySQL 删除已经是事实，不能对客户端谎称“整个事务回滚了”。协调事件会让 Worker 在 Redis 恢复后重新读取 MySQL，并删除陈旧投影。

### 6.2 拉黑与解除

拉黑依靠 `(user_id, blocked_id)` 唯一键解决并发重复；第二个并发请求映射为 1207，而不是数据库 500。

解除拉黑使用幂等 `DELETE`：不存在也成功。

私聊 Lua 同时检查：

```lua
SISMEMBER blacklist:{sender} receiver
SISMEMBER blacklist:{receiver} sender
```

任意一个为 1，就返回私聊错误码 4002。

### 6.3 真正的数据库分页

申请和好友列表都不是“先查全部，再在 Go 里切片”，而是：

```sql
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?
```

另做一次 `COUNT(*)` 得到 total。HTTP 返回：

```json
{
  "items": [],
  "pagination": {
    "total": 0,
    "offset": 0,
    "limit": 20,
    "has_more": false
  }
}
```

`has_more` 的计算是：

```text
offset + 当前页条数 < total
```

SQL 直接 JOIN `users`，把返回的 `nickname` 填为非空昵称、为空时回退 username，并补齐头像，避免每个好友再查一次用户的 N+1 问题。在线态收集本页所有 ID 后用 Redis `MGET` 批量读取；Redis 暂时失败时统一降级为 `online=false`，MySQL 好友列表仍能返回。

## 7. Redis 为什么不能当真相

假设 A、B 在 MySQL 已经是好友，Redis 因重启被清空。旧实现的 Lua 只看：

```text
EXISTS friend:A:B
EXISTS friend:B:A
```

两个 key 都没有，于是把真实好友误判成“不是好友”。

反过来，如果 MySQL 已删除好友，但 Redis 的旧 key 因写失败没有删掉，Lua 又可能错误放行。

所以关系数据的定义必须是：

```text
MySQL = 权威事实
Redis = 为 Lua 准备的、可丢失、可重建投影
```

## 8. loaded marker：区分“空”和“没加载”

只看 Redis Set 是否为空会有歧义：

```text
情况 1：用户确实没有黑名单
情况 2：Redis 刚被清空，还没从 MySQL 加载
```

因此增加：

```text
friend_loaded:{uid}
blacklist_loaded:{uid}
group_member_loaded:{gid}
```

含义是：

- marker 不存在：不知道，必须回源 MySQL。
- marker 存在、集合为空：已经确认，业务事实就是空。

即使用户没有任何好友，也会写 `friend_loaded:{uid}=1`。否则每次消息都会反复查询 MySQL，形成缓存穿透。

## 9. 按需回源怎样防止缓存击穿

Redis 清空后，100 个并发请求可能同时发现 marker 不存在。如果全部查询 MySQL，就叫缓存击穿。

实现为每个资源抢一把短锁：

```text
cache_warm_lock:friends:1
cache_warm_lock:blacklist:1
cache_warm_lock:group_members:8
```

流程：

```text
检查 loaded marker
    ↓ 不存在
SET lock random-token NX PX 5000
    ├── 抢到：再次检查 marker -> 查 MySQL -> 原子替换 -> 写 marker
    └── 没抢到：短暂轮询 marker，等待抢到锁的实例完成
```

释放锁时不是直接 `DEL`，而是 Lua 先比较随机 token。这样旧请求超时后，不会误删后来请求刚拿到的新锁。这把锁不只用于缓存穿透；启动预热、fast path 和多个 Worker 都走同一把 owner 锁，因此同一资源不会同时有两个 Redis 替换。

私聊业务在执行 Lua 前调用：

```go
cacheTruth.EnsurePrivateAccess(ctx, senderID, receiverID)
```

它会保证双方好友投影和双方黑名单投影都已加载。群聊调用：

```go
cacheTruth.EnsureGroupAccess(ctx, groupID)
```

注意：聊天 Service 还属于后续 MSG 任务；实现它时必须先调用这两个入口，不能直接裸调消息 Lua。

## 10. 为什么还需要事务协调事件

按需回源解决“Redis 被清空”，但没有解决“MySQL 提交后，更新 Redis 的那一刻网络断了”。

错误做法：

```text
COMMIT MySQL
DEL Redis 失败
只打日志，然后永久忘记
```

正确做法是事务 Outbox 思路。业务事务里同时插入：

```text
cache_reconcile_events(resource_type='friends', resource_id=A)
cache_reconcile_events(resource_type='friends', resource_id=B)
```

核心状态和事件要么一起提交，要么一起回滚。提交后仍会立即更新 Redis，这叫 fast path；协调事件则是故障兜底。本项目的 fast path 不会拿着旧动作直接 `SET/DEL`，而是和 Worker 一样再读一次 MySQL 当前真相。

### 10.1 为什么事件不保存 `SET` 或 `DEL`

事件表达的是：

```text
“请重新核对 friends:A 的当前真相”
```

而不是：

```text
“请执行一条很久以前产生的 SET friend:A:B”
```

Worker 收到事件后重新查询 MySQL。即使旧事件延迟到删除好友之后才执行，它也不会执行旧 `SET`，而是重建“现在已经不是好友”的投影。但只说“重新读”还不够：读完与写 Redis 之间仍可能插入新事务，所以还需要下面的并发排序。

### 10.2 Worker 如何避免多个实例重复抢事件

Worker 用数据库行锁和租约领取一批事件：

```sql
... FOR UPDATE SKIP LOCKED
```

领取后写入随机 `lock_token`。成功时只有持有同一 token 的 Worker 能标记完成；失败则记录错误并按指数退避重新排队。某实例崩溃后，租约超时，其他实例可以重新领取。

### 10.3 怎样防止“旧快照晚写”

先看危险时序：

```text
Worker 1 读到旧好友列表 S1，暂停
用户删除好友，MySQL 提交新状态 S2
Worker 2 把 S2 写入 Redis
Worker 1 恢复，又用 S1 覆盖 Redis   <- 错
```

当前代码用两个串行点阻止它：

1. 同一 resource/owner 的预热、fast path 和 Worker 先抢同一把 Redis 资源锁。
2. `WithinCacheSnapshot` 开 MySQL 事务，对 `users` 或 `groups` owner 行执行 `SELECT ... FOR UPDATE`，在持有行锁时读快照并完成 Redis 原子替换，然后才 COMMIT 释放行锁。

好友/黑名单业务事务也会锁同一用户行。所以新事务要么在快照之前完成，要么等旧快照已经写完 Redis 后再提交，不会出现上面的交叉。

这个学习项目选择了“短时持有 MySQL owner 行锁，直到 Redis 替换完成”的简单方案。大规模系统可以改用事务内单调 generation + Redis fencing token，减少 Redis 故障时占用数据库锁的时间。

## 11. 三类缓存如何原子重建

### 11.1 好友

重建用户 A 时只替换：

```text
friend:A:*
```

不会擅自删除 `friend:B:A`，因为那个方向属于 B 的 MySQL 行和 B 的投影。`friend_owner_index:{uid}` 记录该 owner 的所有 friend key；在线 reconciliation 与启动 `Warm` 的替换成本和该用户的好友数量相关，不在 Lua 内使用会阻塞 Redis 的 `KEYS`。只有显式严格 `cachectl rebuild` 会在锁内走增量 SCAN，以清理 owner index 外的人工孤儿。私聊前会确保 A、B 两个 owner 都已加载，所以最终两向 key 都存在。

### 11.2 黑名单

在一个 Redis 事务中删除旧 Set、写新成员、写 loaded marker。空黑名单也会写 marker。

### 11.3 群成员

一个原子 Lua 同时维护：

- `group_members:{gid}` 成员 Set。
- `group_member_info:{gid}` 角色/禁言 Hash。
- `user_groups:{uid}` 用户到群的反向 Set。
- `group_reverse_owner_index:{gid}` 记录需要清理/重写反向 Set 的 owner。
- `group_member_loaded:{gid}` marker。

禁言不缓存一个永久的 `muted=true`，而是缓存 Unix 毫秒 `muted_until`。群消息 Lua 用 Redis `TIME` 比较截止时间，到期后同一份缓存会自动允许发言。

巡检不仅比较成员 ID，还分别检查 Set 成员和 Hash field，再比较角色、禁言截止时间以及反向 `user_groups`。这能发现“Hash 还在但 Set 丢了，Lua 把真实成员误判为非成员”的局部损坏。

## 12. 在线状态为什么也要“所有权”

每个连接有随机 `connectionID`，Redis 保存：

```text
conn:{uid}   = connectionID
online:{uid} = connectionID
```

两个 key 的 TTL 是 60 秒，服务每 20 秒发送 Ping 并续租。

考虑同一账号新登录：

```text
新连接写入 connectionID=new
旧连接收到 kick 后断开
```

如果旧连接无条件 `DEL online:{uid}`，它会把新连接的在线状态也删掉。当前实现的续租和释放都先比较：

```text
GET conn:{uid} == 我自己的 connectionID ?
```

旧连接发现 owner 已经是 new，只能结束自己，不能删除新租约。这就是“带所有权的租约”。

好友列表用一次 `MGET online:{id1} online:{id2} ...` 批量补在线态，不逐个请求 Redis。

## 13. WebSocket 事件不是事实

三个事件契约已经在 Go 和 TypeScript 同步：

```text
friendApply
friendAccepted
presence
```

例如申请事件：

```json
{
  "type": "friendApply",
  "data": {
    "requestId": 10,
    "fromUserId": 1,
    "username": "alice",
    "message": "你好",
    "createdAt": "2026-08-31T12:00:00Z"
  }
}
```

前端收到后不会把事件直接当永久数据，而是让 TanStack Query 重新请求 HTTP：

- `friendApply`：刷新 `friend-requests`。
- `friendAccepted`：刷新 `friends` 和 `friend-requests`，建立私聊会话身份。
- `presence`：更新好友分页缓存和已有会话在线态。

如果用户离线，WS 帧不会保存，但 MySQL 申请/好友关系已经保存。首次连接和每次重连都会主动失效两个 Query，所以关键状态不会丢。

当前 Hub 已完成好友事件所需的握手、JWT、单连接替换、心跳和定向推送；聊天帧分派仍属于后续 `WS-001`/`MSG`。当前 Compose 只运行一个 app 实例，`Hub.Notify` 只能查找本进程连接；将来水平扩容前必须增加 Redis Pub/Sub 或 RabbitMQ fanout。Redis presence 租约只表示在线，不是跨节点事件通道。

## 14. 用 PowerShell 手工走一遍接口

先启动依赖和服务：

```powershell
Set-Location 'E:\IT\my_IM'
docker compose up -d

Set-Location 'E:\IT\my_IM\backend'
go run ./cmd/server -c configs/config.local.yaml
```

在另一个终端注册并登录两名用户：

```powershell
$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$aliceName = "alice_$suffix"
$bobName = "bob_$suffix"

$alice = Invoke-RestMethod 'http://localhost:18080/api/v1/auth/register' -Method Post -ContentType 'application/json' -Body (@{username=$aliceName;password='secret1'} | ConvertTo-Json)
$bob = Invoke-RestMethod 'http://localhost:18080/api/v1/auth/register' -Method Post -ContentType 'application/json' -Body (@{username=$bobName;password='secret1'} | ConvertTo-Json)

$aliceLogin = Invoke-RestMethod 'http://localhost:18080/api/v1/auth/login' -Method Post -ContentType 'application/json' -Body (@{username=$aliceName;password='secret1'} | ConvertTo-Json)
$bobLogin = Invoke-RestMethod 'http://localhost:18080/api/v1/auth/login' -Method Post -ContentType 'application/json' -Body (@{username=$bobName;password='secret1'} | ConvertTo-Json)

$aliceHeaders = @{Authorization="Bearer $($aliceLogin.data.access_token)"}
$bobHeaders = @{Authorization="Bearer $($bobLogin.data.access_token)"}
```

Alice 申请 Bob：

```powershell
$apply = Invoke-RestMethod 'http://localhost:18080/api/v1/friend/request' -Method Post -Headers $aliceHeaders -ContentType 'application/json' -Body (@{to_user_id=$bob.data.user_id;message='我们加个好友吧'} | ConvertTo-Json)
```

Bob 查询并接受：

```powershell
Invoke-RestMethod 'http://localhost:18080/api/v1/friend/requests?limit=20&offset=0' -Headers $bobHeaders

Invoke-RestMethod 'http://localhost:18080/api/v1/friend/accept' -Method Post -Headers $bobHeaders -ContentType 'application/json' -Body (@{request_id=$apply.data.request_id} | ConvertTo-Json)

Invoke-RestMethod 'http://localhost:18080/api/v1/friend/list?limit=20&offset=0' -Headers $aliceHeaders
```

Bob 拉黑、解除：

```powershell
Invoke-RestMethod 'http://localhost:18080/api/v1/friend/block' -Method Post -Headers $bobHeaders -ContentType 'application/json' -Body (@{blocked_id=$alice.data.user_id} | ConvertTo-Json)

Invoke-RestMethod 'http://localhost:18080/api/v1/friend/unblock' -Method Post -Headers $bobHeaders -ContentType 'application/json' -Body (@{blocked_id=$alice.data.user_id} | ConvertTo-Json)
```

## 15. 缓存管理命令

在 `backend` 目录执行：

```powershell
go run ./cmd/cachectl -c configs/config.local.yaml -action rebuild -scope all
go run ./cmd/cachectl -c configs/config.local.yaml -action audit -scope all
```

范围可选：

- `friends`
- `blacklists`
- `groups`
- `all`

`rebuild` 只把 MySQL 投影到 Redis；绝不会用 Redis 覆盖 MySQL。它是面向运维的“严格重建”：先取得该 owner 的资源锁，再清除 loaded/index marker，让下一次替换用增量 SCAN 找出并删除 owner index 外的人工孤儿。这个路径可能较慢，所以应用启动使用 `Warm`，线上 fast path 使用 `ReconcileNow`，两者都保留有界 owner index。

`audit` 输出检查数、不一致数和详情。完全一致时退出码为 0，发现不一致时退出码为 2，命令本身出错时为非零错误退出。

你可以做一个安全的故障实验：只删除自己测试用户的已知 key 和 marker，然后再次执行 audit/rebuild。不要在有其他数据的环境随意 `FLUSHDB`。

Compose 显式使用 Redis `noeviction`。loaded marker 解决的是 FLUSHDB、整组 key 丢失或正常原子替换，本身不是完整性校验和。如果只删一个 `friend:*` 或 Hash field，却故意保留 marker，按需入口不会自动猜到它已损坏；定期 `audit` 会发现这种业务投影差异。只损坏内部 owner index、业务 key 仍正确时未必形成 audit mismatch，但显式严格 `rebuild` 仍会重建索引并通过增量 SCAN 清理索引外孤儿。这里的 SCAN 不在 Lua 中执行，也不走线上请求路径。

## 16. 如何运行测试

普通测试不依赖 Docker：

```powershell
Set-Location 'E:\IT\my_IM\backend'
go test ./...
go vet ./...
```

前端：

```powershell
Set-Location 'E:\IT\my_IM\frontend'
npm.cmd test
npm.cmd run build
```

真实 MySQL/Redis 集成测试：

```powershell
Set-Location 'E:\IT\my_IM'
docker compose up -d mysql redis

Set-Location 'E:\IT\my_IM\backend'
$env:MYIM_INTEGRATION = '1'
go test ./internal/api ./internal/service -run 'TestFriend(HTTPIntegration|DockerIntegration)|TestCacheTruthDockerIntegration' -count=1 -v
```

这些集成测试不会 `FLUSHDB`，只清理它们创建的唯一测试用户、关系和明确 Redis key。它们覆盖：

- 并发交叉申请。
- 并发/重复接受与双向两行。
- 分页字段和资料 JOIN。
- 拉黑保留好友、双向 Lua 拦截。
- 删除 Redis key 后按需回源。
- 缓存巡检与即时修复。
- 旧快照晚写与 fast path 的并发排序。
- 群成员正反向投影。
- 群成员 Set/Hash 单边丢失巡检与禁言自动到期。
- 旧 WS 连接不能删除新连接租约。

## 17. 小白最容易踩的坑

### 坑 1：看到 Redis 快，就先写 Redis 再慢慢写 MySQL

好友是强一致业务。如果 Redis 成功、MySQL 失败，缓存里会出现不存在的好友。正确顺序是 MySQL 事务先确定事实，Redis 只做提交后的投影。

### 坑 2：把两个 INSERT 连着写，就以为是原子操作

没有事务时，第一个成功、第二个失败完全可能发生。原子不是“代码挨在一起”，而是数据库保证一起提交或一起回滚。

### 坑 3：只用唯一键处理反向申请

定向唯一键挡得住 A→B 重复，挡不住 B→A。必须给“无方向的用户对”建立共同串行点，本项目选择按顺序锁两名用户。

### 坑 4：缓存没有 key，就等于业务不存在

key 不存在还可能是 Redis 重启。loaded marker 让程序知道该回源，不能直接下业务结论。

### 坑 5：事务提交后 Redis 失败，就向用户返回失败

这会让用户以为没有成功，重试后却发现关系已经存在。应该承认 MySQL 已成功，用协调事件修 Redis。

### 坑 6：用 WS 事件直接维护永久好友状态

网络断开就会丢帧。WS 适合“提醒刷新”，MySQL + HTTP 才负责离线恢复。

### 坑 7：旧连接断开时无条件删除在线 key

新连接可能已经接管。所有续租和删除都必须比较 connectionID 所有权。

### 坑 8：以为 Worker “现查 MySQL”就天然没有竞态

查完 MySQL 到覆盖 Redis 之间仍有时间窗口。必须有 owner 锁或 generation/fencing 把“读快照 + 落缓存”整体排序。

## 18. 建议你的学习顺序

第一次阅读时完成这五个小练习：

1. 在 `FriendService.SendRequest` 中逐条找到 1201、1202、1203、1204 的返回位置。
2. 在 `AcceptRequest` 中画出 pending、accepted、rejected 三个分支。
3. 暂时在测试里让第二个好友 INSERT 返回错误，观察事务为什么不会留下第一行。
4. 阅读 `EnsurePrivateAccess`，说出 marker、warm lock、MySQL loader 各自解决什么问题。
5. 对照 Go payload 与 `frontend/goim-ws-types.ts`，确认三个事件名和 camelCase 字段完全一致。

最后尝试用自己的话回答：

> 如果接受好友已经提交，但 Redis 此刻断电，系统为什么最终还能恢复？

标准思路是：双向好友和协调事件已经一起落进 MySQL；客户端得到的成功是真实的；Redis fast path 虽失败，Worker 恢复后会领取事件，在 owner 锁保护下重新读取 MySQL 当前关系并覆盖 Redis。只有 marker 缺失时，按需入口才会立即回源；如果是“旧投影 + marker 仍在”，则依靠 fast path 或并行启动的 Worker 修复，不应把 marker 夸大成完整性校验和。

当你能独立讲清这段话，就已经掌握了本阶段最重要的能力：不把“缓存里的样子”误认为“业务世界的真相”。
