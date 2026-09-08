# 数据库与中间件契约

本文件记录从 `E:\IT\IM` 实际迁移、Repository、Lua 和 Consumer 代码审计出的事实。业务实现时以本文件和 `backend/scripts/migrations` 为基线，不以旧设计文档中的示例键名为准。

## MySQL：13 张上游表 + 4 张 MyIM 表

| 表 | 主键与关键字段 | 现有索引/约束 | 业务备注 |
| --- | --- | --- | --- |
| `users` | 自增 ID、username、password_hash、资料字段 | username UNIQUE | 两用户并发操作按 ID 升序锁行 |
| `friend_requests` | from/to、message、status | 定向 pair UNIQUE、目标用户稳定分页索引 | 重新申请会替换同向终态旧行并生成新 ID |
| `friendships` | user_id、friend_id | pair UNIQUE、`(user_id,created_at,id)` | 一段好友关系存双向两行 |
| `groups` | name、owner_id、max_members | idx_owner | owner 与成员 role=2 双份表达 |
| `group_tombstones` | group_id、原 owner_id、dissolved_at | group_id PRIMARY、解散时间索引 | 永久记录已解散 ID，防旧 Redis 快照恢复成员权限；不保存可展示群资料 |
| `group_members` | group/user、role、muted_until | group-user UNIQUE、group/user 索引 | role：0 成员、1 管理员、2 群主 |
| `message_id_allocators` | namespace、next_id、updated_at | namespace PRIMARY、`next_id <= 2^53` CHECK | MySQL 事务预留全局互斥号段；`message` 同时服务私聊和群聊 |
| `private_messages` | 统一生成器 ID、client_msg_id、sender/receiver、content | ID 安全整数 CHECK、sender-client UNIQUE、会话/接收方时间、FULLTEXT | client_msg_id 为空时兼容历史消息 |
| `group_messages` | 统一生成器 ID、client_msg_id、group_seq | ID 安全整数 CHECK、sender-client、group-seq UNIQUE；group-time、FULLTEXT | group-seq 可从持久层恢复 |
| `msg_revoked` | 自增 ID、msg_id、conv_id、operator | conv-msg UNIQUE、idx_msg | 多态关联，唯一键保证撤回幂等 |
| `moments` | author、content、JSON media、visibility | author-time、time | 默认 2；历史值 1 在 011 中升级为 2 |
| `moment_likes` | moment/user | pair UNIQUE、idx_moment | 唯一键支持异步幂等写 |
| `moment_comments` | moment/user、content | moment-time | Repository 按 ID 排序，索引不覆盖 |
| `blacklist` | user、blocked | pair UNIQUE | 拉黑保留好友；任一方向拉黑都阻止私聊 |
| `user_settings` | user UNIQUE、通知、预览、JSON mute_list | user UNIQUE + FK | 唯一有物理外键的表 |
| `message_user_states` | user、conv、msg、deleted_at | 三列联合主键、用户删除时间和会话消息索引 | “仅对当前用户删除”的持久状态 |
| `cache_reconcile_events` | resource_type、resource_id、状态/租约/重试 | 待处理领取索引、资源索引 | 与关系事务一起写入的可靠缓存协调事件 |

除 `user_settings.user_id -> users.id` 外，其他关联都由应用维护：

- 用户：好友申请、好友、群主/群成员、消息发送者/接收者、动态作者、点赞/评论者、黑名单。
- 群组：群成员、群消息。
- 动态：点赞、评论。
- `msg_revoked.msg_id` 通过 `conv_id` 判断关联私聊或群聊，是多态逻辑关联。

## 必须使用事务的边界

以下操作在新实现中不能拆成多个裸 Repository 调用：

1. 接受好友申请：锁定申请、校验接收者、更新状态、创建双向好友行。
2. 删除好友：删除双向好友行并产生可重试的缓存协调事件。
3. 拉黑/解除：变更黑名单、终结旧申请并产生缓存协调事件。
4. 创建群：创建 `groups`、创建群主成员行。
5. 转让群主：旧群主降级、新群主升级、更新 `groups.owner_id`。
6. 解散群：写永久墓碑、删除全部成员和群资料、写缓存协调事件。
7. 删除动态：删除评论、点赞、动态主体，并在提交后清缓存。
8. 修改消息状态：持久撤回/个人删除状态与搜索可见性保持一致。

原代码在前四项存在非事务或事务不完整的风险，任务清单已经单列修正。

## 迁移现状

当前完整保留上游 9 个文件，并追加 011、012、013、014：

```text
001_create_users.sql
002_create_friendships.sql
003_create_groups.sql
004_create_messages.sql
005_create_moments.sql
006_create_misc.sql
008_create_user_settings.sql
009_moment_comments_auto_increment.sql
010_moment_comments_auto_increment_guard.sql
011_foundation_schema.sql
012_cache_truth.sql
013_group_tombstones.sql
014_message_id_allocator.sql
```

注意：

- 编号缺 007，这是上游历史，不是复制遗漏。
- 005 已把 `moment_comments.id` 建为自增，009 和 010 又重复修正，属于历史补丁。
- Docker 不再挂载 `/docker-entrypoint-initdb.d`；服务启动和 `cmd/migrate` 共用同一个迁移器。
- `CREATE TABLE IF NOT EXISTS` 不会把旧表升级到新字段定义。
- 早期上游 DDL 没有逐表声明 Engine/charset，依赖 Compose 默认值；MyIM 新增表已显式声明 InnoDB/utf8mb4。

迁移器使用 `schema_migrations`、SHA-256 校验和与 dirty 标记，提供 `up/status`。最终阅读基线位于 `backend/scripts/baseline/000_final_schema.sql`，但实际升级始终走历史迁移。

## 已在 011 完成的 Schema 修正

- 为两张消息表增加 `client_msg_id` 和 sender-client 唯一键。
- 新增 `message_user_states(user_id, conv_id, msg_id, deleted_at)`。
- 将 `group_messages(group_id, group_seq)` 改为 UNIQUE。
- 评论与好友请求索引覆盖时间/id 分页，并删除被 UNIQUE/宽索引覆盖的冗余索引。
- 统一 Go 非指针字段对应的 NOT NULL/default，并把 visibility 默认值统一为 2。

## 已在 012 完成的好友/缓存修正

- 为好友列表增加 `(user_id, created_at, id)` 稳定分页索引。
- 新增 `cache_reconcile_events`。业务事务记录的是“重新读取当前真相”，不是可能过期的 Redis `SET/DEL` 指令。
- Worker 使用行锁租约领取事件；失败按指数退避重试，晚到事件也只会读取 MySQL 最新状态。

## 已在 013 完成的群解散墓碑

- 新增 `group_tombstones(group_id, owner_id, dissolved_at)`，与实时群删除处于同一事务。
- 群 ID 使用自增且不复用；墓碑是永久的“这个 ID 已解散”事实，不随普通协调事件完成而删除。
- 启动预热、`cachectl audit/rebuild` 枚举活动群与墓碑的并集，因此 Redis 恢复旧快照后仍能发现并清空旧成员投影。
- 群消息授权前先查询主键墓碑；这多一次很小的 MySQL 点查，换来运行中 Redis 回滚时也不会信任旧的正缓存。以后若优化，必须用可证明等价的版本机制，不能直接删掉这次校验。

## 已在 014 完成的消息 ID 高水位

- 新增 `message_id_allocators`，`next_id` 表示尚未预留的第一个 ID。
- 升级时从私聊、群聊、撤回记录和用户消息状态引用的历史最大 ID 之后开始，不覆盖已有编号。
- 多实例通过 `SELECT ... FOR UPDATE` 串行预留互不相交的号段；提交后才允许使用，未用完的号段永不回收。
- 消息 ID 上限为 `9,007,199,254,740,991`，保证当前 TypeScript `number`/JSON 数字契约精确。
- 两张消息表也增加同一安全范围 CHECK，Repository 在执行 SQL 前再次拒绝 nil、非正数和越界 ID，防止未来 Consumer 绕开生成器写入坏值。
- allocator 行丢失时生成器失败关闭，禁止根据消息表 `MAX(id)` 在线重建，因为 MQ 中可能还有已发号但未落库的消息。
- 旧、新发号算法没有共同协调点；已有旧消息生产者的升级必须停写、排空旧 MQ、确认持久化、执行 014 后再只启动新版本，禁止滚动混跑。当前聊天发送尚未实现，首次启用不存在这段兼容窗口。

仍留给业务任务决定：

- 决定消息全文搜索使用 `MATCH ... AGAINST` 还是普通 LIKE；当前 FULLTEXT 索引没有被原查询使用。

## Redis：实际键规范

| 键 | 类型 | 用途/策略 |
| --- | --- | --- |
| `inbox:{uid}` | ZSet，score=Unix ms | 用户私聊收件箱，member 为消息 JSON |
| `outbox:{gid}` | ZSet | 群共享发件箱 |
| `conv_list:{uid}` | ZSet | 会话摘要；原更新会扫描旧 member，后续需优化 |
| `unread:{uid}` | Hash | field=convID，value=未读数 |
| `group_read_pos:{uid}` | Hash | field=群 convID，value=group_seq 水位 |
| `group_members:{gid}` | Set | 群成员 ID；重建时与反向集合原子同步 |
| `user_groups:{uid}` | Set | 用户加入的群；群缓存重建时维护反向投影 |
| `group_member_info:{gid}` | Hash | field=uid；JSON 保存 role 与 Unix 毫秒 `muted_until`，Lua 用 Redis TIME 判断到期 |
| `friend:{uid}:{fid}` | String | 双向好友缓存，无 TTL |
| `friend_loaded:{uid}` | String | 即使好友为空，也表示该用户好友投影已从 MySQL 加载 |
| `friend_owner_index:{uid}` | Set | 此 owner 拥有的 friend key 后缀；在线替换走有界索引，严格运维重建走增量 SCAN，均不使用阻塞式 `KEYS` |
| `friend_owner_index_loaded:{uid}` | String | owner index 已完成兼容迁移；严格运维重建会在资源锁内先清除此 marker |
| `blacklist:{uid}` | Set | 当前用户主动拉黑的 ID；私聊 Lua 检查两个方向 |
| `blacklist_loaded:{uid}` | String | 区分“确定为空”和“Redis 未加载” |
| `group_member_loaded:{gid}` | String（十进制整数） | MySQL 快照的预期成员数；Set/Hash 数量均匹配才视为已加载 |
| `group_reverse_owner_index:{gid}` | Set | 需要维护 `user_groups:{uid}` 的用户 ID，供有界原子替换 |
| `group_reverse_owner_index_loaded:{gid}` | String | 旧缓存反向关系已完成一次增量 SCAN 迁移；严格运维重建会先清除此 marker |
| `cache_warm_lock:{resource}:{id}` | String，短 TTL | 按需回源的带随机所有权锁，防缓存击穿 |
| `refresh:{jti}` | Hash，TTL=令牌寿命 | 刷新令牌 family、用户、到期时间 |
| `refresh_user:{uid}` | Set，TTL | 用户的 refresh jti 索引，用于改密/登出全部撤销 |
| `msg_dedup:{sender}:{clientMsgID}` | String，TTL 300s | 客户端消息去重 |
| `group_seq:{gid}` | Counter | 群消息序号 |
| `online:{uid}` / `conn:{uid}` | String，TTL 60s | 值为 connectionID；续租、删除都先比较所有权 |
| `timeline:{uid}` | ZSet | 动态收件箱 |
| `moment_outbox:{author}` | ZSet | 动态寄件箱 |
| `moment:big_users` | Set | 大用户集合 |
| `moment:likes:{id}` | Set | 点赞用户 |
| `moment:like_count:{id}` | String/Counter | 点赞数 |
| `moment:like_loaded:{id}` | String | 点赞缓存已预热标记 |
| `moment:like_lock:{id}` | String，短 TTL | 防缓存击穿锁 |

好友、群成员、黑名单都以 MySQL 为唯一真相，Redis 只是 Lua 使用的可重建投影。当前已经有三层恢复：服务启动异步预热、消息校验前按 loaded marker 回源、事务协调事件后台重试。活动群和永久墓碑都会参与群投影的预热/巡检；群消息授权还会先查 MySQL 墓碑，从而拒绝一份“结构完整但来自解散前”的旧 Redis 正缓存。管理命令 `cmd/cachectl` 还能执行全量重建和只读一致性巡检。

关系写入采用“事务 Outbox + 提交后 fast path”：核心状态与协调事件在同一 MySQL 事务提交；随后 fast path 也不直接执行旧 `SET/DEL`，而是重新读 MySQL。同资源 Redis 锁串行多 Worker，MySQL `users/groups` owner 行用 `FOR UPDATE` 锁到 Redis 原子替换完成，因而旧快照不能在新状态之后落地。立即更新失败不会谎称 MySQL 回滚，Worker 会重试。

GROUP-001 建群就是这条规则的一个完整例子：`groups`、群主 `group_members(role=2)` 与 `cache_reconcile_events(resource_type='group_members')` 在同一事务提交，之后 `ReconcileGroupMembers` 尽力立即重建 Redis。群列表和群详情仍读 MySQL；`user_groups:{uid}` 只是消息权限检查所需的可重建反向投影，不承担权威列表查询。

GROUP-002 成员添加/移除沿用同一规则。事务必须先 `SELECT groups ... FOR UPDATE`，再检查操作者与目标成员；添加还会按 ID 升序锁双方 `users` 行，在锁内确认好友关系并执行 `COUNT + INSERT`。群行锁把同群并发邀请串行化，防止两个请求同时抢到最后一个名额。成员变化和 `cache_reconcile_events(resource_type='group_members')` 同事务提交，提交后只调用全量 `ReconcileGroupMembers`，不使用可能乱序的旧 Redis 增量 Add/Remove。

成员分页从 MySQL 查询：`group_members JOIN users` 补齐 `username/avatar_url`，按 `role DESC, joined_at ASC, id ASC` 稳定排序，`COUNT(*)` 提供真实总数。`limit` 最大 100；非成员不能读取。Redis 的 `group_members:{gid}`、`group_member_info:{gid}` 与 `user_groups:{uid}` 由同一次原子重建共同维护。

GROUP-003 的角色与禁言仍然只更新 `group_members`：角色请求只允许 0/1，不能借此写入群主角色 2；`muted_until=NULL` 表示解禁，未来 UTC 截止时间表示禁言，过期值在审计上可以保留但不会继续生效。任免管理员只允许真实 `groups.owner_id`，禁言时群主可管理管理员/成员，管理员只可管理普通成员。UPDATE 与群成员协调事件同事务提交，完整重建保证改 role 不丢 muted_until、解禁也不丢 role。

GROUP-004 转让群主按 `groups -> 旧群主 member -> 新群主 member` 顺序加锁，在同一事务把旧群主降为 role=0、把新群主升为 role=2 并清空其 `muted_until`、更新 `groups.owner_id`，最后写 `group_members` 协调事件。退群同样先锁群和自己的成员行；真实 owner_id 返回 1306，管理员/普通成员则删除自己的成员行并写协调事件。提交后完整重建会同时修正成员 Set、角色/禁言 Hash 和用户反向群 Set；WS 只发送提交后的刷新提示，不参与权威事务。

GROUP-005 解散群先锁 `groups` 并只认真实 `owner_id`，删除前保存通知成员，然后在同一事务写 `group_tombstones`、批量删除 `group_members`、删除 `groups` 并写 `group_members` 协调事件。`group_messages` 和已有撤回记录不删除，只作服务端审计历史；成员快照已删除，所以当前不向原成员开放解散后历史查看。DELETE 对已不存在群幂等成功，但只有查到墓碑的真实重试才再清 Redis；随机 ID 不产生扫描和负键。后台对账读取空成员真相，清理正反向成员投影并写 `group_member_loaded=0` 负缓存；只有确认群行不存在才额外删除 Redis `outbox:{gid}` 与 `group_seq:{gid}`。`msg_dedup` 依靠 300 秒 TTL，用户级 `conv_list/unread/group_read_pos` 留给消息同步任务按现行成员关系过滤。

群成员 loaded marker 从旧布尔字符串升级为预期基数。`EnsureGroupAccess` 以 O(1) 的 `SCARD/HLEN` 同时校验 Set 与 Hash；老缓存中多成员群的 marker=`1` 会自动触发回源并被改写为真实数量。Lua 对“Set 中是成员但 Hash 元数据缺失”的竞态安全拒绝，避免缓存局部丢失绕过禁言。

好友重建只替换 `friend:{owner}:*`，不会擅自删除另一用户拥有的方向；群成员重建会同时维护 Set、Hash、`user_groups` 和有界 owner index，不在 Lua 中运行全库 `KEYS`。

调用私聊 Lua 前必须先执行 `CacheTruthService.EnsurePrivateAccess(sender, receiver)`；调用群聊 Lua 前执行 `EnsureGroupAccess(groupID)`。统一生成器先从 MySQL 持久号段取得候选 ID，再把它传给 Lua；Lua 独立返回 Redis 毫秒时间。因此清空 Redis 后的第一次请求会回源，但不会重置或复用消息 ID。

本项目把 Redis 开为 AOF 并显式使用 `noeviction`，但 AOF 不能替代上述重建逻辑。好友/黑名单 loaded marker 能识别整组 key 丢失；群成员 marker 还会比较 Set、Hash 与预期人数，因此单边删除成员或 Hash field 时，下一次 `EnsureGroupAccess` 会自动回源。同基数的错误替换、JSON 内容损坏或反向关系漂移仍由 `cachectl audit` 报告，再用严格 `rebuild` 修复。只损坏内部 owner index、业务投影仍正确时未必形成 audit mismatch，但严格重建仍会主动重建该索引：它在同资源锁内清除 loaded/index marker，使下一次替换执行增量 SCAN。启动预热与在线 fast path 不这样做，仍保持与单个 owner 关系数量成正比。

刷新令牌已采用一次性轮换：服务验证 JWT 后用 Redis `WATCH` 检查 `refresh:{oldJTI}`，在同一事务中删除旧会话和用户索引成员，再写入同一 family 的新 JTI。两个并发刷新只有一个能提交，另一个映射为业务码 1106。修改密码会先按 `refresh_user:{uid}` 撤销全部刷新会话，再更新 bcrypt 哈希，避免 Redis 故障时留下仍有效的旧刷新令牌。

MSG-000 已把上游 `Unix毫秒 * 1000 + 同毫秒 INCR` 替换为“MySQL 持久高水位 + 实例内号段”。消息 ID 只保证全局唯一，不编码时间、不保证跨实例按发送时间递增；时间使用 Lua 返回的 Redis `TIME`。`group_seq` 当前只保证同一 Redis 有效状态内的单群原子递增，跨恢复由 MSG-007 补齐。完整证明、边界、故障分析与接缝测试见 `docs/MESSAGE_ID_TUTORIAL.md`。

## RabbitMQ：4 个持久主队列 + 各自 DLQ

| 队列 | 当前目标 |
| --- | --- |
| `private_msg_persist` | 私聊持久化、双方收件箱、在线推送 |
| `group_msg_fanout` | 群消息持久化、发件箱、成员会话/未读、在线推送 |
| `moment_push` | 动态 Feed 写扩散 |
| `like_persist` | 点赞事件攒批、幂等落库 |
评论选择同步写 MySQL，原来没有生产/消费实现的 `comment_persist` 已删除。每个主队列都有 `{queue}.dlq`，死信交换机为 `my_im.dlx`。

发布继续使用默认 Exchange、routing key 等于队列名，并已加入 durable/persistent、Publisher Confirm、mandatory return、超时、断线重连、有限退避重试与 DLQ。消费者业务仍需在后续任务补齐：

- 消费者幂等状态。
- 私聊/群聊重投后的未读计数幂等。
- 数据库写成功但 ACK 丢失时的重复主键处理。
- `MQ-002` 处理晚到群消息前必须检查 `group_tombstones`；已解散群不得重新写 `outbox/group_seq/conv_list/unread`。

`/ready` 已检查 RabbitMQ 连接与主队列；消费者处理语义由 `MQ-001`、`MQ-002` 和 `MSG-007` 完成。
