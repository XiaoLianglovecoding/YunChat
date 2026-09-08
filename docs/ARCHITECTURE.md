# 架构说明

## 事实基线

本骨架以 `E:\IT\IM` 的实际代码和迁移文件为准，而不是完全照搬其中已漂移的设计文档：

- 前端：React 19、TypeScript、Vite 8、Tailwind CSS、Zustand、TanStack Query。
- 后端：Go 1.24、Gin 单体。
- 数据组件：MySQL 8.4、Redis 7.2、RabbitMQ 3.13。
- 原代码实际有 7 个 Service、4 个已实现 Consumer、13 张 MySQL 表；MyIM 另增用户消息状态表、缓存协调事件表、群解散墓碑表与消息 ID 高水位表。
- 开发期前后端分离；生产镜像把 `frontend/dist` 交给 Gin 托管。

## 目标依赖方向

```text
React/Vite
  ├── REST /api/v1 ─────────────┐
  └── WebSocket /ws ────────────┤
                                 v
cmd/server -> api/ws -> service -> repository ports
                    \-> conn       ├── MySQL
                                  ├── Redis + Lua
                                  └── RabbitMQ
                                           |
                                           v
                                       consumers
                                           |
                                  MySQL / Redis / WS push
```

约束：

1. `model` 和 `protocol` 不依赖上层。
2. `service` 只依赖 Repository 接口，不直接持有数据库客户端。
3. `api` 负责传输层校验和响应映射，不承载业务规则。
4. `cmd/server` 是唯一业务服务装配入口；`cmd/migrate`、`cmd/cachectl` 只为各自运维动作做轻量装配。
5. 消息、动态 Feed、点赞等高并发链路通过 Redis/MQ；账户、好友、群资料等强一致操作以 MySQL 事务为主。

## 模块职责

| 模块 | 当前骨架 | 最终职责 |
| --- | --- | --- |
| `api` | 账户/头像/好友/群管理 Handler，其余路由 501 | DTO、参数校验、调用 Service、错误映射 |
| `middleware` | CORS、JWT、请求日志/指标 | JWT、限流、追踪、恢复 |
| `messageid` | MySQL 持久号段、并发安全统一发号器 | 保证私聊/群聊 ID 全局唯一与浏览器数字精度 |
| `service` | 账户、头像、好友、群管理、缓存真相用例 | 权限、事务编排、缓存一致性 |
| `repository` | MySQL/Redis/MQ 接口 | 隔离存储和消息中间件 |
| `ws` | JWT 升级、单连接替换、心跳租约、好友与群变更事件 | 补齐聊天帧分派和跨实例 fanout |
| `conn` | 保留的包边界 | 后续按规模决定是否从 `ws.Hub` 拆出连接管理器 |
| `consumer` | 4 个实际队列名 | 手动 ACK、幂等消费、消费失败计数 |
| `infra` | MySQL/Redis/RabbitMQ、DLQ、Confirm、健康检查 | 客户端初始化、可靠发布、反序关闭 |

## 目标消息流

### 私聊

```text
客户端 msg
  -> WebSocket 鉴权与参数校验
  -> CacheTruthService：按 loaded marker 从 MySQL 补齐双方好友/黑名单投影
  -> 统一 Generator：从 MySQL 持久号段取得 serverMsgID
  -> Redis Lua：好友/黑名单、去重、独立 timestamp
  -> RabbitMQ private_msg_persist
  -> Consumer 幂等处理
       ├── 双方 inbox / conv_list / unread
       ├── MySQL private_messages
       └── 在线 WebSocket 推送
```

### 群聊

```text
客户端 msg
  -> CacheTruthService：缺失时从 MySQL 补齐群成员/角色/禁言投影
  -> 统一 Generator：从同一个 MySQL 持久号段取得 serverMsgID
  -> Redis Lua：成员/禁言、去重、独立 timestamp、group_seq
  -> RabbitMQ group_msg_fanout
  -> Consumer 幂等处理
       ├── 群 outbox
       ├── 成员会话与未读
       ├── MySQL group_messages
       └── 在线成员推送
```

### 消息 ID

私聊和群聊共享 `message_id_allocators(namespace='message')`。各实例通过 MySQL 行锁事务预留一个默认含 65,536 个 ID 的互不相交号段，热路径只在本地互斥区内递增。号段提交后永不回收，所以实例崩溃只产生空洞；算法不读系统时间、也不读 Redis，时钟回拨和 Redis 恢复都不能复用 ID。

消息 ID 限制在 JavaScript 安全整数 `2^53-1` 以内，只表示身份。Lua 单独返回 Redis 毫秒时间，群 Lua 另分配当前 Redis 状态内的单群 `group_seq`，其跨恢复规则由 MSG-007 完成。多实例持有不同号段时，ID 不保证按发送时间全局递增；Redis `TIME` 也可能回拨，因此后续同步不能把二者直接当作不会回退的到达序号。MSG-004 必须增加持久单调同步水位或证明等价协议。完整证明与基准见 `docs/MESSAGE_ID_TUTORIAL.md`。

### 朋友圈

普通用户发布后写个人寄件箱并通过 `moment_push` 扇出到好友 `timeline`；大用户只写寄件箱，读取时将收件箱与关注的大用户寄件箱合并。点赞在 Redis 原子变更，经 `like_persist` 批量落库。

### 账户与鉴权

```text
注册/登录
  -> AuthHandler：只处理 HTTP DTO
  -> AuthService：校验、bcrypt、业务错误
  -> MySQL users（账户真相）
  -> Token Manager：签发 access + refresh
  -> Redis refresh:{jti} + refresh_user:{uid}

保护路由
  -> Authorization: Bearer <access_token>
  -> JWT 中间件：算法/签名/issuer/exp/token_type
  -> 强类型 UserID 注入 Gin Context
  -> 对应业务 Handler

刷新
  -> 验证 refresh JWT
  -> Redis WATCH 原子删除旧 jti、写新 jti
  -> 返回新 access + 新 refresh
  -> 旧 refresh 再次使用返回 1106
```

### 好友与缓存真相

```text
发送申请
  -> FriendHandler：JWT 用户 + DTO/分页参数
  -> FriendService：业务规则
  -> MySQL 事务：按较小用户 ID 先锁行
       ├── 排除自己、黑名单、已有好友、同向/反向 pending
       └── 删除同向终态旧申请，插入新 ID 的 friend_requests
  -> 提交后 friendApply（仅刷新提示）

接受申请
  -> MySQL 同一事务
       ├── 锁用户和申请
       ├── pending -> accepted
       ├── INSERT IGNORE 双向 friendships
       └── 写 cache_reconcile_events
  -> 提交后 Redis fast path
  -> friendAccepted WebSocket 提示

Redis 修复
  -> 启动预热 / 请求按需回源 / cachectl 手工重建
  -> Reconciler 领取事务协调事件
  -> 同资源 Redis 锁串行；MySQL 锁 owner 行并读取当前状态
  -> 原子覆盖 Redis 投影并写 loaded marker
  -> Redis 完成后才释放 MySQL owner 行锁，旧快照不能晚写覆盖新状态
```

WebSocket 事件不是业务事实。用户离线时可能错过 `friendApply` 或 `friendAccepted`，但申请和好友行已经在 MySQL；重连后前端主动失效好友 Query 并通过 HTTP 恢复最终状态。
当前好友事件是单应用实例内定向推送；跨实例 fanout 属于后续 WS-001/OPS。

### 群创建与资料

```text
POST /group
  -> GroupHandler：取得 JWT userID，绑定 name/notice
  -> GroupProfileService：Unicode 长度校验与空白归一化
  -> MySQL 同一事务
       ├── 锁定并确认创建者仍存在
       ├── INSERT groups
       ├── INSERT group_members(role=2)
       └── INSERT cache_reconcile_events(group_members, groupID)
  -> 提交后尽力重建该群 Redis 成员投影
```

`GET /group/list` 始终通过 `group_members JOIN groups` 查询 MySQL，只返回当前用户有成员行的群；不能按 `owner_id` 猜测，也不能把可能尚未完整预热的 `user_groups:{uid}` 当成列表真相。详情只允许群成员读取。资料更新按 `groups -> group_members` 顺序加锁，只更新 `name/notice`；权限来自真实 `groups.owner_id` 或成员 `role=1`，不会把一条异常的额外 `role=2` 当成群主。

建群提交后的 Redis 刷新是加速，不是业务真相。刷新失败时 HTTP 仍返回成功，因为同一 MySQL 事务里的协调事件会由后台 Worker 重试；详见 `docs/GROUP_TUTORIAL.md`。

### 群成员管理

```text
POST/DELETE /group/:groupID/member...
  -> GroupHandler：校验 JWT 用户、路径 ID 与请求体
  -> GroupCoreService：锁 groups 行，检查真实群主/管理员权限
  -> 添加：锁双方 users 行 -> 校验好友/重复/容量 -> INSERT member(role=0)
  -> 移除：锁目标 member -> 检查群主/管理员权限矩阵 -> DELETE member
  -> 同一 MySQL 事务写 cache_reconcile_events(group_members, groupID)
  -> COMMIT 后 ReconcileGroupMembers 全量重建正向 Set、角色 Hash 与反向 Set
```

同一个群的所有成员写入都先锁 `groups` 行，因此 `COUNT + INSERT` 在容量边界不会被两个并发邀请同时穿透。真实群主只认 `groups.owner_id`；群主可移除管理员和普通成员，管理员只能移除普通成员，任何人都不能通过移除接口删除真实群主。`GET /group/:groupID/members` 只允许当前成员读取，使用 MySQL JOIN 补齐用户名/头像并提供 `limit/offset` 分页；Redis 只服务快速权限投影，不承担权威成员列表。

前端以每页 100 条循环收齐当前最多 500 人，并让聊天页与管理抽屉共享同一个 Query Key。完整设计、错误码与手工验证见 `docs/GROUP_MEMBER_TUTORIAL.md`。

### 群角色与禁言

```text
PUT /group/:groupID/member/:memberID/role
PUT/DELETE /group/:groupID/member/:memberID/mute
  -> 锁 groups -> 操作者 member -> 目标 member
  -> 角色：只有真实 owner_id 可把非群主设为 role=0/1
  -> 禁言：群主可管 admin/member，admin 只可管 member
  -> UPDATE role 或 muted_until + 同事务 cache_reconcile_event
  -> COMMIT 后按完整 MySQL 快照重建 group_member_info
```

禁言保存绝对截止时间而非永久布尔值。群消息 Lua 使用 Redis `TIME` 与缓存中的 Unix 毫秒 `muted_until` 比较，仍在期限内返回 `5002`，到期后无需定时任务即可自动放行。`group_member_loaded:{gid}` 保存预期成员数；`EnsureGroupAccess` 同时比较成员 Set 和信息 Hash 的数量，任一独立丢失都会回源。若检查后到 Lua 执行前 Hash field 又消失，Lua 也会 fail-closed，避免把缺失元数据误当作“未禁言”。详见 `docs/GROUP_ROLE_MUTE_TUTORIAL.md`。

### 群主转让与退群

```text
PUT /group/:groupID/owner
  -> 锁 groups -> 旧群主 member -> 新群主 member
  -> 校验操作者等于真实 owner_id，目标已经在群内
  -> 旧群主 role=0 + 新群主 role=2/解除禁言 + groups.owner_id 更新
  -> 同事务写 group_members 协调事件
  -> COMMIT 后重建 Redis，并向当前成员发送 groupUpdated

POST /group/:groupID/leave
  -> 锁 groups -> 自己的 member
  -> 真实群主返回 1306；管理员/普通成员删除自己的成员行
  -> 同事务写协调事件，COMMIT 后重建 Redis
  -> 退出者收到 groupRemoved，剩余成员收到 groupUpdated
```

群主转让的 `owner_id`、旧角色和新角色必须一起提交，任何一步失败都回滚；新群主同时解除已有禁言，避免产生无人可以解禁的真实群主。Redis 与 WebSocket 都是提交后的投影/提示：Redis 失败由协调事件重试，WS 丢帧由 HTTP 群列表、详情和成员列表补偿。当前 Hub 仅推送本实例在线连接，不宣称跨实例可靠通知。完整说明见 `docs/GROUP_TRANSFER_LEAVE_TUTORIAL.md`。

### 群解散、容量与异常恢复

```text
DELETE /group/:groupID
  -> 锁 groups，权限只认 owner_id
  -> 删除前读取成员 ID，供提交后 WS 扇出
  -> 写永久 group_tombstones
  -> 批量 DELETE group_members -> DELETE groups
  -> 同事务写 group_members 协调事件
  -> COMMIT 后快速清缓存，并发送 groupRemoved(dissolved)
```

DELETE 使用期望状态语义：群已不存在时返回成功，不重复写事件或通知；只有存在永久墓碑的真实解散重试才再次清缓存，从未存在的随机 ID 不触发 Redis 扫描或写负键。`group_messages` 不参与删除，只作为服务端审计历史保留；当前成员行已删除，因此不能据此承诺原成员仍可查看历史。

成员上限默认 500，`AddMember` 继续在群行锁内执行 `COUNT + INSERT`。同群的邀请与解散也由这把锁串行，所以并发请求只能产生“邀请先完整提交再被解散”或“解散先提交、邀请看到 1302”两种结果。

Reconciler 对已删除群读取空成员真相，原子清理成员 Set/Hash、`user_groups` 与反向索引，并保留 `group_member_loaded=0` 负缓存；只有再次确认群行不存在时才删除 `outbox/group_seq`，避免误清一个仍存在但成员异常为空的群。启动预热、巡检和重建会枚举活动群与永久墓碑；消息授权前还会先查墓碑，因此运行中切回旧 Redis 快照也不能复活已解散群权限。未来 MQ 消费者同样必须先检查墓碑，不能让晚到消息重建 `outbox`。本机 500 人单实例 WS 扇出 5 次样本约为 1.0～1.6 ms/轮；该结果是进程内编码/入队基线，不代表公网延迟。完整说明见 `docs/GROUP_DISSOLVE_RECOVERY_TUTORIAL.md`。

启动预热调用 `CacheTruthService.Warm`，沿用有界 owner index；运维 `cachectl rebuild` 调用严格 `Rebuild`，会在资源锁内重置索引 marker，并通过增量 SCAN 清理索引外人工孤儿。这样日常请求不承担全库扫描成本，显式修复又能兑现审计结果。

## 当前安全策略

- 注册、登录、刷新和公开头像读取无需 access token；其余 41 个业务路由统一经过 JWT 中间件。
- 未实现的受保护业务只有在鉴权成功后才返回结构化 501，缺失或无效 Token 先返回 401。
- 登录对不存在用户执行 dummy bcrypt；客户端只看到统一的 1105，不泄露账号是否存在。
- refresh token 在 Redis 一次性轮换；改名和改密会按用户索引撤销旧刷新会话。
- 头像同时校验扩展名、MIME 和大小，使用随机名、路径边界校验与同目录原子 Rename。
- `/ready` 只有在 MySQL、Redis、RabbitMQ 全部可用时返回 200。
- 6 个 Lua 脚本只有一套 Go 内来源，启动预加载并核对 SHA。
- WebSocket 在线键保存 connectionID；旧连接只能续租/删除自己的租约，不会把新登录误标为离线。
- 拉黑保留好友关系，私聊 Lua 检查双方黑名单；解除拉黑幂等，但不会复活旧申请。

## 已确认的源码/文档漂移

实现时以右列为准：

| 旧文档说法 | 实际源码 |
| --- | --- |
| 8 个服务、3 个消费者 | 7 个服务、4 个已实现消费者 |
| `msg_id_global` | MySQL `message_id_allocators` 持久号段；私聊、群聊共用统一生成器 |
| `group_list:{uid}` | `user_groups:{uid}` |
| `dedup:{convID}:{clientMsgID}` | `msg_dedup:{senderID}:{clientMsgID}` |
| `group_read_pos:{uid}:{gid}` | `group_read_pos:{uid}` Hash，field 是 convID |
| Lua 同时写收件箱/未读 | 当前消息 Lua 做规则检查、去重、时间戳/群序号；统一 ID 在调用 Lua 前生成，Consumer 写收件箱 |
| 所有写都 Redis 优先、MQ 落库 | 账户、好友、群组、动态主体、评论、设置大量同步写 MySQL |
| Go/TS WebSocket 类型已同步 | 已同步 friendApply/friendAccepted/presence 和 groupAdded/groupRemoved/groupUpdated 常量与联合类型 |

第三阶段已经修复：

- 私聊 Lua 所需 `blacklist:{uid}` 由业务 fast path 与协调事件共同维护，并检查双向拉黑。
- 群成员 Set、角色/禁言到期时间 Hash 与用户反向群集合可以从 MySQL 原子重建；Lua 用 Redis TIME 判断禁言是否到期。
- Redis 数据丢失后会启动预热或按需回源，并可用 `cachectl` 重建/巡检。
- 旧消息 ID 算法已删除；MySQL 持久号段能承受多实例、时钟回拨和 Redis 恢复，且时间戳已与 ID 解耦。

仍需后续任务处理：
- `comment_persist` 因没有 Publisher/Consumer 已从 MyIM 拓扑删除，评论采用同步 MySQL 写。
- 外置 Lua 已删除，`internal/redis/lua_*.go` 是唯一来源。
- 原 Dockerfile 构建 `./cmd`，真实入口是 `./cmd/server`；本骨架已修正。
