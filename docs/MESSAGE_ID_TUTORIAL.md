# 消息 ID 生成方案：从“会撞号”到可证明唯一

本文对应开发清单 `MSG-000`。目标不是只写出一个“看起来随机”的数字，而是让私聊和群聊在高并发、多应用实例、系统时钟回拨、Redis 清空或恢复旧快照以后，仍然不会拿到重复的服务端消息 ID。

## 1. 先分清四种容易混淆的编号

| 名称 | 谁生成 | 解决什么问题 | 能否表示时间/顺序 |
| --- | --- | --- | --- |
| `clientMsgID` | 客户端 | 客户端重试时去重 | 不能 |
| `serverMsgID` | 本文的统一生成器 | 跨私聊、群聊唯一标识一条服务端消息 | 不能，必须当作不透明 ID |
| `timestamp` | Redis `TIME` | 记录服务端接收时间、参与同步游标 | 可以表示时间 |
| `group_seq` | 群消息 Lua | 当前 Redis 状态内的单群原子序号 | 只在单群内有效；跨 Redis 恢复由 `MSG-007` 补齐 |

最重要的一句话是：**ID 负责“你是谁”，时间戳负责“你何时发生”，群序号负责“你在这个群排第几”。** 不要再从 `serverMsgID` 反推时间。

## 2. 为什么旧算法不安全

旧 Lua 使用：

```text
ID = Unix 毫秒 × 1000 + 当前毫秒内的序号
```

例如某毫秒是 `100`：

```text
第 1 条：100 × 1000 + 1    = 100001
第 1001 条：100 × 1000 + 1001 = 101001
下一毫秒第 1 条：101 × 1000 + 1 = 101001
```

两个不同请求得到了一样的数。旧脚本实际上在序号超过 `999` 时直接报错，避免了真正写入重复 ID，但这只是把“撞号”变成“高峰期拒绝消息”；而且去重键已经先写入，客户端五分钟内重试仍可能被当成重复。

它还有两个根本问题：

- 时间回拨后，算法可能重新进入用过的毫秒区间。
- `msg_id_seq:{millis}` 在 Redis，Redis 清空、回档或恢复旧快照后，计数历史可能丢失。

## 3. 方案比较与最终选择

| 方案 | 优点 | 当前项目的问题 |
| --- | --- | --- |
| Redis `INCR` | 简单、快、多实例共享 | Redis 回档后可能复用旧值，无法满足本任务的恢复证明 |
| Snowflake | 吞吐高、通常大致有序 | 需要可靠 worker ID 和回拨处理；标准 64 位结果会超出前端 JavaScript 安全整数 |
| UUID/ULID | 不依赖中心计数器 | 要把 BIGINT、Go `int64`、JSON 和 TS `number` 全改为字符串；唯一性通常是概率保证 |
| 每条消息访问 MySQL 序列 | 容易证明唯一 | 每条消息都加一次数据库延迟和热点写，吞吐不合适 |
| **MySQL 持久化号段 + 进程内递增** | **确定唯一、热路径快、不读时钟、不依赖 Redis、保留数字协议** | 允许跳号；跨实例不按发送时间严格递增 |

最终采用最后一种方案。

## 4. 用“整卷取票”理解号段

把 MySQL 想成唯一的票仓，应用实例是多个售票窗口。

数据库中只有一行核心状态：

```text
namespace = message
next_id   = 1
```

`next_id` 表示“第一个还没有被任何实例预留的 ID”。默认每次取 `65,536` 张票：

```text
实例 A 锁行：next_id 由 1 改成 65537，获得 [1, 65536]
实例 B 随后锁行：next_id 由 65537 改成 131073，获得 [65537, 131072]
```

事务提交以后，A、B 才能使用各自号段。平时生成 ID 只是 Go 进程内的一次加一，不访问 MySQL；号段用完才再取一卷。

实例 A 如果只用了 `1` 就崩溃，`2..65536` 会成为空洞。新实例从 `65537` 以后开始，绝不能捡回空洞。**跳号是唯一性换来的安全结果，不代表消息丢失。**

## 5. 一条消息现在怎样拿到 ID

```text
消息请求
  -> RedisRepo 调用统一 Generator.Next
       -> 本地号段还有票：互斥锁内 next++
       -> 本地号段用完：MySQL 行锁事务预留下一段
  -> 把候选 serverMsgID 传入 Redis Lua
  -> Lua 做好友/黑名单或成员/禁言检查
  -> Lua 写 5 分钟去重键
  -> 群聊 Lua 再原子增加 group_seq
  -> Lua 返回原 ID + 独立的 Redis 毫秒 timestamp
```

如果权限检查或去重失败，已经取出的 ID 会被放弃。这同样只是空洞，不会影响正确性。

对应源码：

- `backend/internal/messageid/generator.go`：并发安全的进程内发号器。
- `backend/internal/messageid/mysql_store.go`：MySQL 号段事务。
- `backend/scripts/migrations/014_message_id_allocator.sql`：持久高水位表和历史 ID 升级起点。
- `backend/internal/repository/redis_repository.go`：统一生成器与消息 Lua 的接缝。
- `backend/internal/redis/lua_private_msg.go`、`lua_group_msg.go`：消费外部 ID，返回独立时间戳。
- `backend/cmd/server/main.go`：全服务只装配一个 `message` 命名空间生成器。

## 6. 为什么可以证明唯一

证明只需要几个不变量：

1. 所有私聊和群聊共用 MySQL 中同一个 `message` 行。
2. `SELECT ... FOR UPDATE` 让多实例的预留事务排队执行。
3. 每个事务先持久化新的 `next_id`，提交成功后才返回号段。
4. 因此任意两个已返回号段都不相交。
5. 一个实例在自己的号段内用互斥锁递增，不会在实例内重复。
6. 崩溃不会降低 MySQL 高水位；重启只会取更高的新段。
7. 生成算法根本不读取墙上时钟，所以时钟向前或向后跳都不能改变发号状态。
8. 生成算法根本不读取 Redis，所以 Redis 清空、重启或恢复旧快照也不能改变发号状态。

即使 ID 已经进入 RabbitMQ、还没落到消息表，它所属的整个号段也早已在 MySQL 提交。这正是不能在故障后简单用 `MAX(message.id)` 重建计数器的原因。

## 7. 为什么上限是 2^53-1

MySQL `BIGINT` 和 Go `int64` 能精确保存更大的整数，但浏览器里的 JavaScript `number` 使用 IEEE-754 双精度浮点数。它能连续、精确表示的最大整数是：

```text
Number.MAX_SAFE_INTEGER = 9,007,199,254,740,991 = 2^53 - 1
```

当前前端、HTTP 和 WebSocket 契约都把消息 ID 当作数字，所以生成器在这个边界失败关闭，绝不返回更大的值。数据库允许 `next_id` 最终等于 `2^53`，它只是“已经耗尽”的高水位哨兵，不会作为消息 ID 发出。

若从 1 开始，即使持续每秒生成 100 万个 ID，也约可使用 285 年。当前迁移还会从四处历史引用的最大值之后开始：

- `private_messages.id`
- `group_messages.id`
- `msg_revoked.msg_id`
- `message_user_states.msg_id`

## 8. 吞吐与号段大小

本阶段冻结的目标是：**已有号段时，单进程内存发号路径至少 100,000 ID/s**。这不是聊天整链路吞吐目标；网络、真实 MySQL 换段延迟、Lua、MQ 和落库的持续压测属于依赖集成测试与 `LOAD-001`。

2026-09-08 在本机 `13th Gen Intel Core i5-13500`、Windows amd64、Go 1.26.3 上执行三次并行基准：

```text
47.45 ns/op   0 B/op   0 allocs/op
50.89 ns/op   0 B/op   0 allocs/op
49.29 ns/op   0 B/op   0 allocs/op
```

取最慢样本换算仍约为 `19,650,226 ID/s`，超过热路径目标约 196 倍。该基准的 Store 是内存替身，只证明本地互斥和递增的成本；它不等于真实 MySQL 换段吞吐，更不等于聊天链路吞吐。

默认号段为 `65,536`。在每秒 10 万 ID 时，理论上平均只需：

```text
100,000 ÷ 65,536 ≈ 1.53 次 MySQL 预留/秒
```

可通过配置调整：

```yaml
message_id:
  segment_size: 65536
```

号段越大，数据库预留次数越少，但实例刚取段就崩溃时可能跳过的 ID 越多；配置上限为 `1,000,000`。

## 9. 故障时会发生什么

| 故障 | 结果 | 为什么仍唯一 |
| --- | --- | --- |
| 单机时钟回拨 | 继续发当前号段 | 算法不读时钟 |
| Redis `FLUSHDB` / 重启 / 回档 | 关系缓存需重建，`group_seq` 恢复待 MSG-007；消息 ID 不复用 | 发号状态只在 MySQL |
| 应用实例崩溃 | 当前未用号段变空洞 | MySQL 高水位已经提交 |
| MySQL 暂时不可用 | 当前号段仍可用；用完后失败 | 不猜测、不回收、不随机补号 |
| 号段事务提交结果不确定 | 本次调用报错，不能使用猜测的号段 | 可能多跳一段，也不能冒险复用 |
| allocator 行丢失/越界 | 失败并提示迁移或耗尽 | 绝不从 1 静默重建 |

服务启动会只读校验 allocator 表、`message` 行和高水位，缺迁移、行丢失、损坏或耗尽都会在监听 HTTP 前失败；它不会为了探测而浪费一个号段。唯一性依赖的根是同一个可写 MySQL 主库。如果未来恢复了较旧的 MySQL 备份，必须先停消息流量、处理 MQ 中的消息，并把高水位人工提升到所有可观察 ID 之上；不能单独回退 `message_id_allocators`。这是后续 `OPS-002` 灾备演练要固化的规则。

## 10. 首次上线与旧版本切换

当前项目的聊天发送 Service 还没有实现，因此第一次启用 014 和新生成器时没有旧消息生产者，直接迁移是安全的。

如果将来要把仍在发送消息的旧版本升级到这个版本，**不能让旧 Lua 发号器和新号段发号器滚动混跑**。迁移只看得到已经持久化的最大 ID，看不到还停留在旧实例内、Redis 或 MQ 中但未落库的未来 ID；两个算法同时运行就没有共同的唯一性协调点。

安全切换顺序必须是：

```text
停止所有旧消息 producer
  -> 排空并核对旧消息 MQ
  -> 确认旧消息都已持久化
  -> 执行 014 迁移，按完整历史最大值建立高水位
  -> 只启动使用新生成器的版本
  -> 恢复消息流量
```

账号、群管理等不发消息的 HTTP 能否继续开放，应由正式发布 runbook 决定；关键是不允许旧、新两种消息发号路径同时接收流量。

切换前还应运行交叉检查，确认旧私聊表和群聊表没有已经重复的历史 ID。014 能保证新 ID 高于所有持久引用，但不会伪造性地“修复”两个旧表之间早已存在的重复编号。

## 11. 一个容易踩的排序坑

号段方案保证全局唯一，但**不保证多实例按真实发送时间全局递增**：A 可能还在使用较低号段，B 已经开始使用较高号段。

因此：

- 展示时间使用 `timestamp`，不能比较 `serverMsgID` 猜先后。
- 群内暂用 Lua 原子增加的 `group_seq`；Redis 恢复后的持久水位由 MSG-007 补齐。
- Redis `TIME` 自己也可能因宿主机校时而回拨，所以当前 `readMessagesAfter` 不能直接当作“无漏消息”的最终实现。
- `MSG-004` 实现同步时需要新增不会回退的持久同步水位/序号，或提供等价且有故障测试的协议；不能只靠 `(Redis timestamp, serverMsgID)` 猜测晚到顺序。

这是选型的明确权衡，不是唯一性漏洞。

## 12. 怎样验证

快速单测覆盖了超过旧上限的突发、同实例多 goroutine、多生成器多实例、重启丢段、存储失败、取消、坏号段和安全整数边界：

```powershell
Set-Location 'E:\IT\my_IM\backend'
$env:GOCACHE = 'E:\IT\my_IM\.cache\go-build'
go test ./internal/messageid ./internal/redis ./internal/repository -count=1
go test ./... -count=1
```

运行基准：

```powershell
go test ./internal/messageid -run '^$' `
  -bench '^BenchmarkGeneratorParallel$' -benchmem -benchtime=3s -count=3
```

Docker 启动后运行真实 MySQL/Redis 接缝测试：

```powershell
Set-Location 'E:\IT\my_IM'
docker compose up -d mysql redis

Set-Location '.\backend'
$env:MYIM_INTEGRATION = '1'
go test ./internal/messageid ./internal/redis `
  -run 'MySQLStore|MessageID|MessageLua|GroupMute' -count=1 -v
Remove-Item Env:MYIM_INTEGRATION
```

真实 MySQL 用例会用两个独立连接池并发取段，证明正确性来自数据库行锁而不是某个 Go 进程锁；测试命名空间独立，结束后只删除自己的行。Redis 用例会把接近 `2^53-1` 的 ID 原样传入 Lua，并验证返回的时间戳落在调用前后的 Redis 时间之间。

如果机器安装了支持 Go race detector 的 C 编译器，还应执行：

```powershell
go test -race ./internal/messageid -count=1
```

## 13. 小白自测题

1. 为什么实例崩溃后不能回收没用完的号段？因为你无法证明其中某个 ID 没有已经发到 MQ 或客户端。
2. 为什么 Redis 开了 AOF 还不够？因为 AOF 也可能丢失、回档或恢复旧状态，不能成为不可复用证明。
3. 为什么允许跳号？ID 的职责是唯一，不是连续；强求连续会把崩溃恢复变得危险或昂贵。
4. 为什么不再写 `timestamp = msgID / 1000`？新 ID 不编码时间，这样才彻底摆脱时钟回拨。
5. MySQL 故障时为什么不随机生成一个备用 ID？那会破坏确定性唯一保证；安全系统宁可明确失败，也不能悄悄撞号。
