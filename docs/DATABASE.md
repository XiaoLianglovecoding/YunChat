# 数据库与中间件契约

本文件记录从 `E:\IT\IM` 实际迁移、Repository、Lua 和 Consumer 代码审计出的事实。业务实现时以本文件和 `backend/scripts/migrations` 为基线，不以旧设计文档中的示例键名为准。

## MySQL：13 张表

| 表 | 主键与关键字段 | 现有索引/约束 | 业务备注 |
| --- | --- | --- | --- |
| `users` | 自增 ID、username、password_hash、资料字段 | username UNIQUE、idx_username | 两个 username 索引重复 |
| `friend_requests` | from/to、message、status | 两个状态索引、定向 pair UNIQUE | 重复申请复用并重置历史行 |
| `friendships` | user_id、friend_id | pair UNIQUE、idx_user | 一段好友关系存双向两行 |
| `groups` | name、owner_id、max_members | idx_owner | owner 与成员 role=2 双份表达 |
| `group_members` | group/user、role、muted_until | group-user UNIQUE、group/user 索引 | role：0 成员、1 管理员、2 群主 |
| `private_messages` | Redis 生成 ID、sender/receiver、content | 会话/接收方时间索引、FULLTEXT | ID 不自增，缺 client_msg_id |
| `group_messages` | Redis 生成 ID、group_seq | group-seq、group-time、FULLTEXT | group-seq 当前不是 UNIQUE |
| `msg_revoked` | 自增 ID、msg_id、conv_id、operator | idx_msg | 多态关联，缺唯一撤回约束 |
| `moments` | author、content、JSON media、visibility | author-time、time | DDL 默认 1，当前语义只接受 2/3 |
| `moment_likes` | moment/user | pair UNIQUE、idx_moment | 唯一键支持异步幂等写 |
| `moment_comments` | moment/user、content | moment-time | Repository 按 ID 排序，索引不覆盖 |
| `blacklist` | user、blocked | pair UNIQUE、idx_user | Redis 黑名单同步在原代码中缺失 |
| `user_settings` | user UNIQUE、通知、预览、JSON mute_list | user UNIQUE + FK | 唯一有物理外键的表 |

除 `user_settings.user_id -> users.id` 外，其他关联都由应用维护：

- 用户：好友申请、好友、群主/群成员、消息发送者/接收者、动态作者、点赞/评论者、黑名单。
- 群组：群成员、群消息。
- 动态：点赞、评论。
- `msg_revoked.msg_id` 通过 `conv_id` 判断关联私聊或群聊，是多态逻辑关联。

## 必须使用事务的边界

以下操作在新实现中不能拆成多个裸 Repository 调用：

1. 接受好友申请：锁定申请、校验接收者、更新状态、创建双向好友行。
2. 删除好友：删除双向好友行并产生可重试的缓存失效事件。
3. 创建群：创建 `groups`、创建群主成员行。
4. 转让群主：旧群主降级、新群主升级、更新 `groups.owner_id`。
5. 删除动态：删除评论、点赞、动态主体，并在提交后清缓存。
6. 修改消息状态：持久撤回/个人删除状态与搜索可见性保持一致。

原代码在前四项存在非事务或事务不完整的风险，任务清单已经单列修正。

## 迁移现状

当前完整保留上游 9 个文件：

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
```

注意：

- 编号缺 007，这是上游历史，不是复制遗漏。
- 005 已把 `moment_comments.id` 建为自增，009 和 010 又重复修正，属于历史补丁。
- Docker 的 `/docker-entrypoint-initdb.d` 只在空 MySQL 数据卷首次初始化时执行。
- `CREATE TABLE IF NOT EXISTS` 不会把旧表升级到新字段定义。
- 当前 DDL 没有逐表声明 Engine/charset，依赖 Compose 的服务器默认值。

`DB-001` 应引入正式 migration runner 和 `schema_migrations`；`DB-002` 再生成干净 baseline。不要在尚未验证已有数据升级路径时删除这些历史脚本。

## 建议的 Schema 修正

- 为消息表增加 `client_msg_id`，至少建立 `(sender_id, client_msg_id)` 唯一键。
- 新增 `message_user_states(user_id, conv_id, msg_id, deleted_at)`，唯一键为 `(user_id,conv_id,msg_id)`，用于“只对当前用户删除”；它是计划中的第 14 张表，不在复制的 13 表历史迁移内。
- 将 `group_messages(group_id, group_seq)` 改为 UNIQUE，并设计 Redis 丢失后的序号恢复。
- 将 `moment_comments` 索引调整为 `(moment_id, id)`。
- 将好友请求列表索引调整为 `(to_user_id, status, created_at)`。
- 删除被 UNIQUE 左前缀覆盖的冗余索引：users、friendships、group_members、moment_likes、blacklist 中共 5 个。
- 决定消息全文搜索使用 `MATCH ... AGAINST` 还是普通 LIKE；当前 FULLTEXT 索引没有被原查询使用。
- 统一空值策略：数据库 NOT NULL/default 与 Go 的非指针 string/bool/time.Time 必须一致。
- 明确 `moments.visibility` 默认值为 2，并保留旧值 1 的读取兼容迁移。

## Redis：实际键规范

| 键 | 类型 | 用途/策略 |
| --- | --- | --- |
| `inbox:{uid}` | ZSet，score=Unix ms | 用户私聊收件箱，member 为消息 JSON |
| `outbox:{gid}` | ZSet | 群共享发件箱 |
| `conv_list:{uid}` | ZSet | 会话摘要；原更新会扫描旧 member，后续需优化 |
| `unread:{uid}` | Hash | field=convID，value=未读数 |
| `group_read_pos:{uid}` | Hash | field=群 convID，value=group_seq 水位 |
| `group_members:{gid}` | Set | 群成员 ID |
| `user_groups:{uid}` | Set | 用户加入的群 |
| `group_member_info:{gid}` | Hash | Lua 需要的角色/禁言信息；原代码没有完整维护 |
| `friend:{uid}:{fid}` | String | 双向好友缓存，无 TTL |
| `blacklist:{uid}` | Set | 私聊 Lua 需要；原代码没有写入 |
| `refresh_session:{jti}` | Hash/String，TTL=令牌寿命 | 刷新令牌 family、用户、消费状态 |
| `refresh_user:{uid}` | Set，TTL | 用户的 refresh jti 索引，用于改密/登出全部撤销 |
| `msg_dedup:{sender}:{clientMsgID}` | String，TTL 300s | 客户端消息去重 |
| `msg_id_seq:{millis}` | Counter，TTL 2s | 同一毫秒内 ID 序号 |
| `group_seq:{gid}` | Counter | 群消息序号 |
| `online:{uid}` / `conn:{uid}` | String，TTL 60s | 在线和活动连接标识 |
| `timeline:{uid}` | ZSet | 动态收件箱 |
| `moment_outbox:{author}` | ZSet | 动态寄件箱 |
| `moment:big_users` | Set | 大用户集合 |
| `moment:likes:{id}` | Set | 点赞用户 |
| `moment:like_count:{id}` | String/Counter | 点赞数 |
| `moment:like_loaded:{id}` | String | 点赞缓存已预热标记 |
| `moment:like_lock:{id}` | String，短 TTL | 防缓存击穿锁 |

缓存恢复是 P0：好友、群成员、黑名单都是 MySQL 真相，但消息 Lua 直接依赖 Redis。Redis 清空后若不回源，已有好友会被判定为非好友，已有群成员会被判定为非成员。需要启动重建、按需回源或可靠变更事件三者中的至少两层保障。

本骨架把 Redis 开为 AOF，但 AOF 不能替代重建逻辑。

上游消息 ID 算法为 `Unix毫秒 * 1000 + 同毫秒 INCR`。当单实例在同一毫秒分配超过 1000 个 ID 时，会与下一毫秒编号区间碰撞；多实例、时钟回拨和 Redis 丢失也没有完整证明。`MSG-000` 必须先选择新的全局 ID 方案，再实现私聊/群聊 Lua。

## RabbitMQ：5 个持久队列

| 队列 | 当前目标 |
| --- | --- |
| `private_msg_persist` | 私聊持久化、双方收件箱、在线推送 |
| `group_msg_fanout` | 群消息持久化、发件箱、成员会话/未读、在线推送 |
| `moment_push` | 动态 Feed 写扩散 |
| `like_persist` | 点赞事件攒批、幂等落库 |
| `comment_persist` | 上游只声明未使用；实现或删除前保持“预留”状态 |

原拓扑使用默认 Exchange、routing key 等于队列名、durable queue、持久消息和手动 ACK。缺少：

- Publisher Confirm 与 mandatory return。
- DLQ、最大重试次数和退避。
- 消费者幂等状态。
- 私聊/群聊重投后的未读计数幂等。
- 数据库写成功但 ACK 丢失时的重复主键处理。

这些必须在 `INFRA-003`、`MQ-001`、`MQ-002` 和 `MSG-007` 中完成后，才能把 `/ready` 改为 200。
