# 架构说明

## 事实基线

本骨架以 `E:\IT\IM` 的实际代码和迁移文件为准，而不是完全照搬其中已漂移的设计文档：

- 前端：React 19、TypeScript、Vite 8、Tailwind CSS、Zustand、TanStack Query。
- 后端：Go 1.24、Gin 单体。
- 数据组件：MySQL 8.4、Redis 7.2、RabbitMQ 3.13。
- 原代码实际有 7 个 Service、4 个已实现 Consumer、13 张 MySQL 表；MyIM 新增第 14 张状态表。
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
4. `cmd/server` 是唯一装配入口。
5. 消息、动态 Feed、点赞等高并发链路通过 Redis/MQ；账户、好友、群资料等强一致操作以 MySQL 事务为主。

## 模块职责

| 模块 | 当前骨架 | 最终职责 |
| --- | --- | --- |
| `api` | 路由和 501 TODO | DTO、参数校验、调用 Service、错误映射 |
| `middleware` | CORS、关闭式鉴权 TODO | JWT、限流、追踪、恢复 |
| `service` | 显式用例接口 | 权限、事务编排、缓存一致性 |
| `repository` | MySQL/Redis/MQ 接口 | 隔离存储和消息中间件 |
| `ws` | 协议再导出 | 升级、分发、错误信封 |
| `conn` | 包边界 | 单用户连接替换、心跳、在线推送 |
| `consumer` | 4 个实际队列名 | 手动 ACK、幂等消费、消费失败计数 |
| `infra` | MySQL/Redis/RabbitMQ、DLQ、Confirm、健康检查 | 客户端初始化、可靠发布、反序关闭 |

## 目标消息流

### 私聊

```text
客户端 msg
  -> WebSocket 鉴权与参数校验
  -> Redis Lua：好友/黑名单、去重、ID
  -> RabbitMQ private_msg_persist
  -> Consumer 幂等处理
       ├── 双方 inbox / conv_list / unread
       ├── MySQL private_messages
       └── 在线 WebSocket 推送
```

### 群聊

```text
客户端 msg
  -> Redis Lua：成员/禁言、去重、消息 ID、group_seq
  -> RabbitMQ group_msg_fanout
  -> Consumer 幂等处理
       ├── 群 outbox
       ├── 成员会话与未读
       ├── MySQL group_messages
       └── 在线成员推送
```

### 朋友圈

普通用户发布后写个人寄件箱并通过 `moment_push` 扇出到好友 `timeline`；大用户只写寄件箱，读取时将收件箱与关注的大用户寄件箱合并。点赞在 Redis 原子变更，经 `like_persist` 批量落库。

## 骨架期安全策略

- 公开业务路由返回结构化 501。
- 所有受保护路由先被 `AUTH-004` 中间件拒绝，避免“先写 Handler、忘记加鉴权”。
- `/ready` 只有在 MySQL、Redis、RabbitMQ 全部可用时返回 200。
- 6 个 Lua 脚本只有一套 Go 内来源，启动预加载并核对 SHA。

## 已确认的源码/文档漂移

实现时以右列为准：

| 旧文档说法 | 实际源码 |
| --- | --- |
| 8 个服务、3 个消费者 | 7 个服务、4 个已实现消费者 |
| `msg_id_global` | `msg_id_seq:{Unix毫秒}`，ID 为毫秒值乘 1000 加序号 |
| `group_list:{uid}` | `user_groups:{uid}` |
| `dedup:{convID}:{clientMsgID}` | `msg_dedup:{senderID}:{clientMsgID}` |
| `group_read_pos:{uid}:{gid}` | `group_read_pos:{uid}` Hash，field 是 convID |
| Lua 同时写收件箱/未读 | 当前消息 Lua 主要做检查、去重和 ID；Consumer 写收件箱 |
| 所有写都 Redis 优先、MQ 落库 | 账户、好友、群组、动态主体、评论、设置大量同步写 MySQL |
| Go/TS WebSocket 类型已同步 | Go 原协议含 friendApply/friendAccepted/presence，TS 联合类型未包含；TS 含 groupAdded/groupRemoved，原 Go 无常量 |

其他必须在开发任务中修复的问题：

- 私聊 Lua 读取 `blacklist:{uid}`，但原业务没有维护该 Redis Set。
- 群聊 Lua 读取 `group_member_info:{gid}`，但原群业务没有完整维护禁言信息。
- Redis 数据丢失后没有从 MySQL 重建好友/群成员缓存。
- `comment_persist` 因没有 Publisher/Consumer 已从 MyIM 拓扑删除，评论采用同步 MySQL 写。
- 外置 Lua 已删除，`internal/redis/lua_*.go` 是唯一来源。
- `millis*1000+同毫秒序号` 在单毫秒超过 1000 条时可能碰撞，必须由 MSG-000 替换。
- 原 Dockerfile 构建 `./cmd`，真实入口是 `./cmd/server`；本骨架已修正。
