# 业务开发任务清单

任务按依赖顺序排列。优先级：P0 阻塞核心链路，P1 构成可用产品，P2 是上线质量。已经落到路由、接口或包边界的 `TODO[ID]` 可用下面的命令定位；Schema、运维和跨模块任务只在本清单中追踪。

```powershell
Set-Location 'E:\IT\my_IM'
rg -n 'TODO\[[A-Z0-9,-]+\]' backend
```

## 完成定义

一个任务只有同时满足以下条件才能勾选：

- TODO Handler 已替换为真实 DTO 绑定、校验、Service 调用和错误映射。
- Service 只依赖接口，并覆盖权限、并发、事务或幂等规则。
- Repository/中间件实现有单元或集成测试。
- HTTP 保持 `{code,message,data}`；WebSocket 保持 `goim-ws-types.ts` 契约。
- 更新本清单和相关文档，不把凭据、Token、上传文件或构建产物提交进仓库。

## 0. 已完成的骨架工作

- [x] **SCAFFOLD-001**：创建可编译、可启动的 Go/Gin 骨架。
- [x] **FRONTEND-000**：原样复制 66 个前端文件和锁文件。
- [x] **CONTRACT-000**：注册上游 42 个业务 HTTP 路由和 WebSocket 入口。
- [x] **DATA-000**：复制 13 张表的 9 个历史迁移及 6 个 Go 模型文件。
- [x] **SAFE-000**：业务端点返回显式 501，受保护端点默认关闭。

## 1. 工程与基础设施

- [x] **FND-001 (P0)**：确定真实 Git module、服务名、数据库名和前端品牌文案。  
  依赖：无。验收：不再包含 `github.com/example/my-im`；配置、镜像和文档命名一致。

- [x] **DB-001 (P0)**：接入版本化迁移工具并在启动/独立命令中提供 `up/status`。  
  依赖：无。验收：使用 `schema_migrations`，对空库和已有库重复执行都安全，失败不会启动业务流量。

- [x] **DB-002 (P1)**：在保留历史升级路径的前提下生成最终态 baseline 并修正索引/空值/visibility。  
  依赖：DB-001。验收：保留上游 13 表并新增 `message_user_states`；升级测试覆盖 001-010；模型、索引和修正项与 `docs/DATABASE.md` 一致。

- [x] **INFRA-001 (P0)**：实现 MySQL 连接池和 `MySQLRepository`。  
  依赖：DB-001。验收：连接/查询超时、最大连接数、事务辅助器、唯一键错误映射和健康检查都有测试。

- [x] **INFRA-002 (P0)**：实现 Redis 客户端、Repository、脚本单一来源和健康检查。  
  依赖：无。验收：不再同时维护“外置 Lua + Go 常量”两套实现；脚本 SHA/加载失败可观测。

- [x] **INFRA-003 (P0)**：实现 RabbitMQ 连接、4 个实际主队列及其 DLQ 和可靠发布。  
  依赖：无。验收：支持 Confirm、mandatory return、超时、断线处理、DLQ 和有限重试；`comment_persist` 明确实现或删除。

- [x] **FND-002 (P0)**：在 `cmd/server` 完成依赖装配与优雅关闭。  
  依赖：INFRA-001/002/003。验收：依赖逆序关闭；`/ready` 检查 MySQL/Redis/MQ 并在全部可用时返回 200。

- [x] **CONTRACT-001 (P0)**：恢复并冻结 HTTP/WS 错误码契约。  
  依赖：无。验收：1001-1703、4001-5003 错误码有类型化 Service 错误映射；HTTP 状态和业务码分别测试；前端枚举同步。

- [x] **OBS-001 (P1)**：结构化日志、request/trace ID、指标和 pprof。  
  依赖：FND-002。验收：日志不含密码/Token/消息正文；能观测 WS 连接、MQ 堆积、消费失败和数据库延迟。

## 2. 账户与鉴权

- [x] **AUTH-001 (P0)**：用户注册。
  依赖：INFRA-001。验收：用户名 3-50 字符、密码至少 6 字符、bcrypt/Argon2 哈希、并发重名映射为 1103。

- [x] **AUTH-002 (P0)**：登录和 TokenPair 签发。
  依赖：AUTH-001。验收：恒时密码校验；JWT 含 `user_id`、`username`、`exp`；失败不泄露用户是否存在。

- [x] **AUTH-003 (P0)**：刷新令牌轮换与失效。
  依赖：AUTH-002、INFRA-002。验收：Redis 保存 jti/family/用户索引；刷新令牌可撤销、一次性轮换、重放被拒绝；扩展前端 refresh 响应与 authStore，使其原子替换新 refresh_token。

- [x] **AUTH-004 (P0)**：JWT 鉴权中间件。
  依赖：AUTH-002。验收：校验算法、签名、签发方和过期时间；注入强类型 userID；所有保护路由都有 401 契约测试。

- [x] **AUTH-005 (P1)**：修改用户名。
  依赖：AUTH-004、INFRA-001。验收：唯一性并发安全；成功后重新签发包含新用户名的 Token。

- [x] **AUTH-006 (P1)**：修改密码。
  依赖：AUTH-003/004。验收：验证旧密码、更新哈希、通过用户刷新会话索引撤销全部现有刷新令牌、审计安全事件。

- [x] **PROFILE-001 (P1)**：头像读取与上传后的资料更新。
  依赖：AUTH-004、UPLOAD-001。验收：`/avatar/:userID` 与前端 `VITE_STATIC_BASE_URL` 契约一致。

## 3. 好友与缓存真相

- [x] **FRIEND-001 (P0)**：发送和分页查询好友申请。
  依赖：AUTH-004、INFRA-001。验收：禁止给自己申请；处理反向/重复/已是好友/黑名单；分页元数据正确。

- [x] **FRIEND-002 (P0)**：接受/拒绝申请。
  依赖：FRIEND-001。验收：接受操作在同一事务中更新申请并创建双向好友行；重试幂等。

- [x] **FRIEND-003 (P0)**：好友列表和删除。
  依赖：FRIEND-002、INFRA-002。验收：双向删除事务化；Redis 好友键在提交后可靠同步；列表补齐昵称、头像、在线态。

- [x] **FRIEND-004 (P0)**：拉黑/取消拉黑。
  依赖：FRIEND-003。验收：MySQL 与 `blacklist:{uid}` 一致；私聊 Lua 能真实拦截；定义拉黑是否同时解除好友。
  已冻结策略：拉黑保留好友关系、拒绝双方待处理申请；任一方向拉黑都禁止私聊，解除拉黑不会恢复旧申请。

- [x] **FRIEND-005 (P1)**：在线状态和好友事件通知。
  依赖：WS-001、FRIEND-002。验收：好友申请/接受/在线变化的 Go 常量、TypeScript 联合类型和前端处理器同步，离线不丢关键状态。
  已完成本任务所需的 WS 鉴权、单连接替换、带所有权的心跳租约与定向事件；聊天帧分派仍保留在 WS-001/MSG 任务。
  当前部署契约是单应用实例；跨实例好友事件 fanout 留给 WS-001/OPS，不把 Redis 在线租约误当成跨节点消息总线。

- [x] **CACHE-001 (P0)**：好友、黑名单、群成员缓存的预热/回源/重建。
  依赖：INFRA-001/002。验收：清空 Redis 后已有关系仍能正确发消息；提供管理命令和一致性巡检。
  故障边界：Compose 使用 `noeviction`；loaded marker 处理整库/整组 key 丢失。业务成员、Set/Hash、角色/禁言和反向关系的投影差异由 `cachectl audit` 显示；显式 `rebuild` 会在资源锁内重置索引迁移标记并用增量 SCAN 清理索引外孤儿。只损坏内部 owner index 不一定单独形成 audit mismatch，但严格重建仍会重建它；启动预热和在线 fast path 只使用有界 owner index。

## 4. 群组

- [x] **GROUP-001 (P0)**：群创建、查询、列表和资料更新。
  依赖：AUTH-004、INFRA-001。验收：建群事务同时创建群主成员；仅有权限者更新；列表仅返回当前成员的群。

- [x] **GROUP-002 (P0)**：添加、移除和分页查询成员。
  依赖：GROUP-001、FRIEND-003、INFRA-002。验收：只邀请好友、容量限制、权限矩阵、MySQL 与双向 Redis Set 一致。
  已冻结规则：真实群主以 `groups.owner_id` 为准；群主可移除管理员/普通成员，管理员只能移除普通成员；添加按群行锁串行化容量检查，成员变更与 `group_members` 协调事件同事务提交，提交后按 MySQL 全量重建 Redis 正反向投影。

- [ ] **GROUP-003 (P1)**：管理员角色与禁言信息。  
  依赖：GROUP-002。验收：不能越权修改同级/群主；增加成员禁言/解禁 HTTP 或 WS 命令；`group_member_info:{gid}` 被完整维护；群 Lua 正确拒绝禁言用户。

- [ ] **GROUP-004 (P0)**：转让群主和退群。  
  依赖：GROUP-002。验收：转让三处状态在同一事务；群主不能直接退出；缓存和 WS 群变更通知一致。

- [ ] **GROUP-005 (P2)**：解散群、成员上限与异常恢复。  
  依赖：GROUP-004。验收：清理逻辑可重试；历史消息保留策略明确；大群扇出有压测数据。

## 5. WebSocket 与消息核心

- [ ] **MSG-000 (P0)**：选择并验证消息 ID 生成方案。  
  依赖：INFRA-001/002。验收：不能直接使用会在单毫秒超过 1000 条时碰撞的 `millis*1000+seq`；新方案在目标吞吐、多实例、时钟回拨和 Redis 恢复下可证明唯一。

- [ ] **WS-001 (P0)**：WebSocket 握手、鉴权、连接管理、心跳和分发。  
  依赖：AUTH-004、INFRA-002。验收：`/ws?token=`；单用户新连接踢旧连接；读写协程无泄漏；online/conn TTL 自动续租。

- [ ] **MSG-001 (P0)**：私聊发送 Lua 与 Service。  
  依赖：MSG-000、WS-001、FRIEND-004、CACHE-001。验收：好友/双向黑名单检查、5 分钟 clientMsgID 去重、调用统一 ID 生成器；返回 serverAck。

- [ ] **MSG-002 (P0)**：群聊发送 Lua 与 Service。  
  依赖：MSG-000、WS-001、GROUP-003、CACHE-001。验收：成员/角色/禁言检查、去重、统一消息 ID 和单群 group_seq 原子分配。

- [ ] **MQ-001 (P0)**：私聊 Consumer。  
  依赖：MSG-001、MSG-007、INFRA-003。验收：双方 inbox/会话、接收方未读、MySQL、在线推送均幂等；坏消息入 DLQ。

- [ ] **MQ-002 (P0)**：群聊 Consumer。  
  依赖：MSG-002、MSG-007、INFRA-003。验收：共享 outbox、成员会话/未读、MySQL、在线推送幂等；成员变化竞态有定义。

- [ ] **MSG-003 (P1)**：送达 ACK、已读 ACK 和未读数。  
  依赖：MQ-001/002。验收：私聊 readStatus 与群 group_read_pos 模型清晰；重复 ACK 不改变最终结果。

- [ ] **MSG-004 (P0)**：断线重连与离线同步。  
  依赖：MQ-001/002、MSG-003。验收：使用 `(timestamp,msgID)` 复合游标；批量边界无重无漏；同步会话和未读。

- [ ] **MSG-005 (P1)**：消息撤回。  
  依赖：MSG-004。验收：发送者、2 分钟时限、私聊双方/群 outbox 原子替换；MySQL 状态与搜索结果同步。

- [ ] **MSG-006 (P1)**：个人删除和消息搜索。  
  依赖：DB-002、INFRA-001、AUTH-004。验收：用 `message_user_states` 持久化每用户隐藏状态且不影响对方；搜索有权限过滤、分页、撤回/删除过滤。

- [ ] **MSG-007 (P0)**：消息与 Consumer 幂等模型。  
  依赖：MSG-000、DB-002、INFRA-003。验收：持久化 client_msg_id 唯一键；ACK 丢失和重投不会重复加未读；group_seq 可从持久层恢复。该任务必须先于 MQ-001/002 完成。

## 6. 朋友圈

- [ ] **MOMENT-001 (P1)**：动态发布、详情、用户列表和删除。  
  依赖：AUTH-004、INFRA-001。验收：visibility 只接受 2/3；媒体 JSON 校验；权限、分页和关联删除正确。

- [ ] **MOMENT-002 (P1)**：收件箱/寄件箱 Feed 和 `moment_push` Consumer。  
  依赖：MOMENT-001、INFRA-002/003、FRIEND-003。验收：普通用户写扩散、大用户读扩散、复合游标合并无重无漏。

- [ ] **MOMENT-003 (P1)**：原子点赞、缓存预热和批量落库。  
  依赖：MOMENT-001、INFRA-002/003。验收：防击穿、重复点赞幂等、取消不为负数、批量 last-action-wins、重投安全。

- [ ] **MOMENT-004 (P1)**：评论创建/删除和展示资料。  
  依赖：MOMENT-001。验收：内容长度、所有者权限、数据库自增 ID、排序索引和删除策略正确；决定是否启用 comment_persist。

- [ ] **MOMENT-005 (P2)**：Feed/点赞清理与冷热策略。  
  依赖：MOMENT-002/003。验收：timeline 与 moment_outbox 上限一致；大用户可降级；删除动态清理全部缓存。

## 7. 设置、文件与前端收尾

- [ ] **SETTINGS-001 (P1)**：用户设置读取和 upsert。  
  依赖：AUTH-004、INFRA-001。验收：首次读取有默认值；布尔/JSON 空值与 Go 模型一致。

- [ ] **SETTINGS-002 (P1)**：会话静音。  
  依赖：SETTINGS-001。验收：convID 格式和归属校验；重复 mute/unmute 幂等；静音真正影响通知策略。

- [x] **UPLOAD-001 (P1)**：头像上传。
  依赖：AUTH-004。验收：字段名 `file`；同时检查 MIME/扩展名/大小；随机文件名、防路径穿越、原子写入；返回相对 URL。

- [ ] **FRONTEND-001 (P1)**：移除 Mock 默认会话和完成真实空状态。  
  依赖：AUTH-002、FRIEND-003。验收：不再跳到 `lin-cheng`；无好友/无会话/Token 失效路径可用。

- [ ] **FRONTEND-002 (P2)**：移动端、生产 sourcemap 与品牌收尾。  
  依赖：核心页面联调。验收：确定最小宽度策略；公开构建不泄露不必要源码；文案和 localStorage key 完成迁移。

## 8. 质量、安全与交付

- [ ] **TEST-001 (P0)**：Repository 与 Service 单元测试。  
  验收：权限、边界、并发、事务回滚和错误码映射覆盖；测试不依赖执行顺序。

- [ ] **TEST-002 (P1)**：MySQL/Redis/RabbitMQ 集成测试。  
  验收：自动创建隔离数据；覆盖 Lua、迁移、Consumer ACK/NACK、缓存清空恢复。

- [ ] **TEST-003 (P1)**：前后端契约和 E2E。  
  验收：注册→加好友→私聊→建群→离线同步→动态完整链路；42 路由与 TypeScript 类型不漂移。

- [ ] **SEC-001 (P0)**：输入限制、限流、上传安全、CORS 和秘密管理。  
  验收：登录/注册/WS/上传独立限额；生产禁止通配 CORS；密钥不在日志和镜像层；依赖漏洞扫描。

- [ ] **LOAD-001 (P2)**：并发和故障压测。  
  验收：给出 WS 连接数、私聊/群聊吞吐、P95/P99、Redis/MQ/MySQL 瓶颈；覆盖 Redis/MQ 重启。

- [ ] **OPS-001 (P1)**：CI、生产镜像、反向代理和发布。  
  验收：`go test`、前端 typecheck/test/build、镜像构建全通过；`/api/v1`、`/ws`、`/uploads` 与 SPA fallback 正确。

- [ ] **OPS-002 (P2)**：备份、恢复、告警和回滚演练。  
  验收：MySQL/RabbitMQ/Redis 的恢复目标明确；有可执行 runbook；完成一次演练并记录结果。

## 推荐里程碑

1. **M1 可注册登录**：DB/INFRA、AUTH-001~004、基础测试。
2. **M2 可加好友私聊**：FRIEND、CACHE、WS、MSG-001、MQ-001、MSG-003/004/007。
3. **M3 可群聊**：GROUP、MSG-002、MQ-002。
4. **M4 社交功能完整**：消息操作、朋友圈、设置、上传。
5. **M5 可上线**：安全、E2E、压测、监控、CI/CD、备份恢复。
