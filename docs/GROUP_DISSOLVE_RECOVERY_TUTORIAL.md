# 群聊第五步教程：解散群、成员上限与异常恢复

这篇教程对应开发清单中的 `GROUP-005`。这一阶段不只是增加一个“解散群聊”按钮，还要回答三个更接近真实生产系统的问题：

1. 群被解散时，哪些数据删除，哪些数据保留？
2. 两个邀请同时争抢最后一个名额时，怎样保证不会超员？
3. MySQL 已成功，但 Redis 或 WebSocket 失败时，系统怎样恢复？

完成后的接口是：

| 功能 | 方法与路径 | 请求体 | 成功响应 |
| --- | --- | --- | --- |
| 解散群聊 | `DELETE /api/v1/group/:groupID` | 无 | `{ "code": 0, "message": "ok" }` |

接口必须携带 access token。操作者只能来自 JWT，客户端不能在请求体中指定“由谁解散”。

## 1. 先区分“退群”和“解散群”

| 操作 | 谁能执行 | 删除什么 | 群是否继续存在 |
| --- | --- | --- | --- |
| 退群 | 管理员或普通成员 | 只删除自己的成员关系 | 是 |
| 解散群 | 真实群主 | 删除群资料和全部成员关系 | 否 |

所以群主有两种选择：

```text
只想离开，但让群继续存在
  -> 先转让群主
  -> 再退群

不希望群继续存在
  -> 直接解散群
```

管理员即使能邀请、移除或禁言成员，也不能解散群。解散权限只认 `groups.owner_id`，不会因为一条异常成员记录写着 `role=2` 就放行。

## 2. 历史消息策略：硬删除实时状态，保留审计历史

本阶段采用：

```text
删除：groups
删除：group_members
新增并永久保留：group_tombstones
保留：group_messages
保留：已经存在的 msg_revoked 记录
```

群资料和成员关系表示“现在还能不能使用这个群”；消息表示已经发生过的历史事实。如果解散事务顺便删除所有消息，误操作会立即销毁历史，事务耗时也会随消息数量增长。

`group_tombstones` 只记录群 ID、解散时的 owner ID 和时间，用来证明“这个 ID 已永久解散”；它不是可展示的群资料，也不记录历史成员。

这里的“消息保留”必须准确理解：MySQL 消息行只作为服务端审计历史保留，不代表原成员解散后还能从普通客户端查看。成员关系已经删除，系统无法再据此证明谁曾属于该群。如果以后要求原成员继续查看历史，必须另增历史成员快照，再设计访问权限，不能只看消息的 `sender_id` 猜测。

保留多久、何时归档或物理删除属于后续消息与运维策略；GROUP-005 不删除历史消息。

## 3. 解散事务具体做什么

```text
BEGIN
  1. SELECT groups ... FOR UPDATE
  2. 检查 operatorID == groups.owner_id
  3. 删除前读取并保存全部成员 ID
  4. INSERT group_tombstones(groupID, ownerID)
  5. DELETE FROM group_members WHERE group_id = ?
  6. DELETE FROM groups WHERE id = ?
  7. INSERT cache_reconcile_events(group_members, groupID)
COMMIT
```

第 3 步非常重要。提交后成员行已经不存在，如果那时才查询“通知谁”，只能得到空列表。因此 Service 必须在事务里保存收件人，提交成功后再发送 `groupRemoved(dissolved)`。

群成员用一条批量 SQL 删除，不循环执行 500 次 DELETE。这样数据库往返更少，事务持锁时间更容易控制。

墓碑、删除和协调事件都在同一事务中：

- 删除或事件任一步失败，整个事务回滚；
- 删除提交成功，就一定留下一条缓存修复凭据；
- Redis 和 WebSocket 不参与 MySQL 的提交判断。

## 4. 为什么所有群写操作都先锁群行

添加、移除、任免、禁言、转让、退群和解散都从同一条 `groups` 行锁开始。它相当于这个群的业务闸门。

邀请和解散同时发生时只有两种完整结果：

```text
邀请先拿锁
  -> 邀请提交
  -> 解散随后读到新成员
  -> 新成员也被删除并收到通知

解散先拿锁
  -> 群和成员被删除
  -> 邀请随后读不到群并返回 1302
```

不会出现“群已经没了，最后一个邀请却插入孤儿成员”的半完成状态。

## 5. DELETE 为什么可以安全重试

DELETE 表达期望状态：“这个群最终不存在”。

```text
客户端发出 DELETE
  -> 服务端事务提交
  -> 200 响应在网络中丢失
  -> 客户端再次发送 DELETE
```

第二次发现群已不存在时仍返回成功。Service 会检查永久墓碑：墓碑存在才再次尽力清缓存，但不会重复写删除事件或通知旧成员。这叫幂等：执行一次或多次，最终状态相同。

对于一个从未存在的 ID，DELETE 也返回相同的 HTTP 状态和响应体；因为没有墓碑，它不会访问 Redis、不会触发兼容 SCAN，也不会写永久负键。这一区分防止已登录用户用大量随机 ID 放大缓存扫描。`GET /group/:groupID` 等查询仍返回 `1302`，只有删除命令采用这种期望状态语义。

## 6. 成员上限到底在哪里保证

新群默认：

```text
groups.max_members = 500
```

前端达到上限时会禁用邀请，但前端只能改善体验。用户可以绕过浏览器手写请求，两个请求也可能在界面刷新前同时到达。

真正的容量检查在 `AddMember` 的 MySQL 事务中：

```text
锁 groups 行
  -> 检查权限、用户、好友关系和重复成员
  -> SELECT COUNT(*) FROM group_members
  -> count >= max_members：返回 1304
  -> 否则 INSERT group_members
```

假设上限是 2、群里已有 1 人，两个请求同时抢最后一个名额：

```text
请求 A：读到 1，插入后变成 2，提交
请求 B：等待 A，随后读到最新的 2，返回 1304
```

因此最终不会出现第 3 人。唯一索引 `(group_id,user_id)` 还会阻止重复插入同一成员。

## 7. MySQL 成功、Redis 失败时为什么不能返回失败

如果 MySQL 已提交解散，HTTP 不能因为 Redis 暂时不可用而声称“解散失败”。正确分层是：

```text
MySQL groups/group_members：当前权威状态
MySQL group_tombstones：永久的已解散事实
cache_reconcile_events：可靠的待修复凭据
Redis：可重建投影和运行时加速
WebSocket：单实例在线刷新提示
```

提交后 Service 先尝试快速对账。失败时 Reconciler 领取协调事件并重新读取当前 MySQL，而不是重放一条旧的“删除某成员”命令。即使事件晚到，它看到的仍是“群不存在、成员为空”，不会把旧成员复活。

协调事件完成后会变成 done，不能永远依赖它被重复领取。因此迁移 013 另建永久墓碑：预热、巡检和重建枚举“活动群 + 墓碑”的并集；`EnsureGroupAccess` 在信任 Redis 正缓存前也先点查墓碑。即使 Redis 在运行中被切回解散前快照，授权也会立即返回“群已解散”，根本不执行 Lua；异步预热或人工重建随后再清掉旧投影。这里选择多一次主键点查来换权限正确性，后续若优化必须有等价的版本证明。

## 8. 解散后 Redis 应是什么状态

成员对账会：

- 从旧成员的 `user_groups:{uid}` 移除群 ID；
- 清空 `group_members:{gid}`；
- 清空 `group_member_info:{gid}`；
- 清空 `group_reverse_owner_index:{gid}`；
- 写 `group_member_loaded:{gid}=0`；
- 保留 `group_reverse_owner_index_loaded:{gid}=1`。

最后两个 marker 不是脏数据，而是“已经确认该群没有成员”的负缓存。正常消息入口会先从 MySQL 点查永久墓碑并直接返回非成员错误，不执行 Lua；即便单独验证 Lua，空成员 Set 也会拒绝，并且不分配消息序号或去重键。

CacheTruth 还会先确认 `groups` 行确实不存在，再幂等删除：

- `outbox:{gid}`；
- `group_seq:{gid}`。

不能只因成员为空就删这两个键：一个仍存在但数据异常的群也可能暂时没有成员，重置仍在使用的 `group_seq` 会造成序号重复。

本阶段不扫描 `msg_dedup:*`：它不包含 groupID，且有 300 秒 TTL。`conv_list/unread/group_read_pos` 属于尚未完成的消息同步层用户投影；当前由 `groupRemoved` 和权威 HTTP 群列表清理前端状态。以后实现 `MQ-002` 时，晚到的旧群消息必须先检查墓碑，不得重新写 `outbox/group_seq/conv_list/unread`。

还要注意一个刻意留给 `MSG-002/MQ-002` 的并发边界：本阶段已经提供永久墓碑和 `EnsureGroupAccess` 的前置拒绝，但消息生产者、消费者尚未实现，所以不能宣称“发送消息与解散群”的完整链路已经线性化。后续消息 Handler 遇到 `ErrGroupDissolved` 或 `ErrGroupDoesNotExist` 必须在调用发送 Lua 前停止；Consumer 在落 MySQL、会话、未读和在线推送前还要重新检查墓碑。这样即使一条消息在解散事务提交前已进入队列，解散后才被消费，也不会重新制造群的运行时状态。

## 9. 删除群后缓存快照和旧备份为什么仍能工作

正常重建会锁住群行，防止旧快照晚写覆盖新事务。解散后实时群行没有了，`WithinCacheSnapshot` 会改锁永久墓碑：

```text
能锁到群行 -> 持锁读取活动群并替换 Redis
锁不到群、能锁到墓碑 -> 持锁读取空成员真相并清理
群和墓碑都没有 -> 从未存在；显式对账才会读空，在线随机 DELETE 不发起对账
其他数据库错误 -> 返回错误，稍后重试
```

项目使用自增 ID，删除的 groupID 不会重新分配给新群；墓碑永久保留，所以删除后的场景不会与同 ID 新群竞态。活动群和墓碑的并集还让启动预热与 `cachectl` 能发现从旧 Redis 备份复活的孤儿投影。

## 10. WebSocket 为什么必须在 COMMIT 后发送

解散成功后，事务内捕获的每位原成员收到：

```json
{
  "type": "groupRemoved",
  "data": {
    "groupId": 100,
    "reason": "dissolved"
  }
}
```

前端会删除群会话、本地消息、群资料和成员 Query，再刷新权威群列表。

通知若在 COMMIT 前发送，而事务最终回滚，客户端就会错误删掉仍存在的群。提交后的通知失败则不影响业务：用户可能离线、连接可能背压，当前 Hub 也只覆盖单应用实例。稳定连接不会凭空知道漏帧；客户端会在下次初始化、重连或主动刷新 `/group/list` 时收敛。

通知逐成员尽力投递，不为 500 个成员无限创建 goroutine。500 人基准在本机 i5-13500 / Windows amd64 上测得：

| 场景 | 500 人一轮耗时 | 内存与分配 |
| --- | --- | --- |
| 全部离线 | 约 0.96～1.42 ms/op | 约 100 KB、2000 allocs/op |
| 在线队列可写 | 约 1.26～1.59 ms/op | 约 100 KB、2000 allocs/op |

这是单进程 CPU 侧编码/入队基线，不等于公网端到端压测。当前每位成员都会单独编码一次，未来群上限明显增大时可再引入“编码一次、批量投递”的 Hub 接口。

## 11. 前端怎样避免误操作和旧响应复活

- 只有 `group.owner_id` 等于当前用户时显示“解散群聊”；
- 群主不显示普通退群按钮，管理员/成员只显示退群；
- 二次确认包含群名、全部成员都会移出以及“不可撤销”说明；
- 成功后关闭抽屉，删除会话、本地消息和群 Query；
- 随即刷新权威群列表，并用 generation 丢弃更早的旧列表响应；
- 收到 `reason=dissolved` 后记录当前登录会话内的群 ID 墓碑；实时 `msg`、`syncBatch`、`convSync` 和普通 addGroup 都不能把它重新建回来；
- `1301` 表示页面中的群主信息已过期：保留本地数据、显示稳定提示并重新查询；
- 前端防御性地把 `1302` 当作重复删除已达到目标。

按钮不是安全边界，后端 Service 的 `owner_id` 校验才是。

## 12. 三层代码怎样分工

`GroupHandler.Disband` 读取 JWT 用户、解析 groupID、调用 Service、返回统一响应。

`GroupServiceImpl.Disband` 负责权限、幂等、固定锁顺序、收件人快照、事务编排和提交后副作用。

Repository 执行窄 SQL，并确保事务回调都绑定到同一个 `sql.Tx`。`UpsertGroupTombstone` 写永久负事实；`DeleteGroup` 只删除群当前资料，故意不删除消息；缓存 Repository 只在 MySQL 确认群消失后删运行时键。

## 13. 错误码速查

| code | HTTP | 场景 |
| --- | --- | --- |
| `1002` | 400 | groupID 或用户 ID 非法 |
| `1301` | 403 | 群存在，但操作者不是真实群主 |
| `1302` | 404 | 查询、邀请等非删除操作访问不存在的群 |
| `1304` | 409 | 邀请时群成员已达 `max_members` |

重复解散不存在的群返回 200，不返回 1302。

## 14. 用 PowerShell 自己调用

```powershell
$base = 'http://localhost:18080/api/v1'
$groupID = 100
$ownerToken = '替换为真实群主 access_token'
$headers = @{ Authorization = "Bearer $ownerToken" }

Invoke-RestMethod -Method Delete `
  -Uri "$base/group/$groupID" `
  -Headers $headers
```

立刻重复同一命令仍应得到 `code=0`。换成管理员 token 解散一个仍存在的群，应得到 HTTP 403 和 `code=1301`。

## 15. 用 SQL 和 Redis 检查

```sql
SELECT * FROM `groups` WHERE id = 100;
SELECT * FROM group_members WHERE group_id = 100;
SELECT group_id, owner_id, dissolved_at
FROM group_tombstones
WHERE group_id = 100;

-- 历史消息应继续存在
SELECT id, group_id, sender_id, group_seq, created_at
FROM group_messages
WHERE group_id = 100
ORDER BY group_seq;

SELECT id, resource_type, resource_id, status, attempts, last_error
FROM cache_reconcile_events
WHERE resource_type = 'group_members' AND resource_id = 100
ORDER BY id DESC;
```

前两条应为空，墓碑查询应恰好有一行，消息查询仍可有结果。协调事件完成前状态为 0/1，对账成功后为 2。

```text
SCARD group_members:100
HLEN group_member_info:100
GET group_member_loaded:100
SCARD group_reverse_owner_index:100
GET group_reverse_owner_index_loaded:100
SISMEMBER user_groups:7 100
EXISTS outbox:100 group_seq:100
```

最终成员 Set/Hash/反向关系为空，`group_member_loaded` 为 `0`，reverse-index marker 为 `1`，`outbox` 和 `group_seq` 不存在。

## 16. 测试分别证明什么

- Service：真实群主权限、异常角色不越权、墓碑与删除一起回滚、幂等重试、随机 ID 不碰缓存、Redis/WS 故障不反转提交。
- Handler/Router：JWT 身份透传、非法 groupID、新 DELETE 路由。
- CacheTruth：活动空群不误删运行时键；墓碑覆盖预热/巡检；已删除群清理失败后可重试；旧正缓存先查墓碑并拒绝，再由 Warm/Rebuild 清空。
- Docker：硬删除、墓碑保留、消息保留、事件修复、旧 Redis 快照恢复、Lua 拒绝、并发双解散、解散与邀请互斥、并发容量最后名额。
- 前端：权限按钮、危险确认、成功/1302 清理、1301 保留状态、`groupRemoved(dissolved)` 以及晚到实时/同步消息不复活。
- Benchmark：固定 500 人测试 Hub 的解散通知 CPU 侧扇出。

验证命令：

```powershell
Set-Location 'E:\IT\my_IM\backend'
$env:GOCACHE = 'E:\IT\my_IM\.cache\go-build'
go test ./...
go vet ./...

$env:MYIM_INTEGRATION = '1'
go test ./internal/service -run '^TestGroup.*DockerIntegration$' -count=1 -v
go test ./internal/ws -run '^$' -bench 'BenchmarkHubDisbandFanout500' -benchmem -count=5

Set-Location 'E:\IT\my_IM\frontend'
npm.cmd run typecheck
npm.cmd test -- --run
npm.cmd run build
```

## 17. 适合小白亲手做的故障实验

1. 让 `DeleteGroupMembers` 返回错误：群、成员、墓碑和事件都保持原状，也没有 WS。
2. 让 `EnqueueCacheReconcile` 返回错误：墓碑和事务里的 DELETE 全部回滚。
3. 让提交后快速对账失败：HTTP 成功、MySQL 群消失、Redis 暂时旧；运行 Reconciler 后收敛到 0 人。
4. 对随机正 ID 重复 DELETE：HTTP 仍成功，但缓存 mock 不应收到调用，Redis 也不新增 marker。
5. 解散后伪造旧成员正缓存：调用 `EnsureGroupAccess` 时直接得到 `ErrGroupDissolved`，消息 Handler 必须映射为 5001 且停止在 Lua 之前；再运行 Warm/Rebuild 会清掉旧投影。
6. 把上限设为 2，同时邀请两个好友：恰好一个成功、一个返回 1304，总人数不超过 2。

这些实验能帮你区分：事务内失败表示权威写没完成；提交后的缓存/通知失败表示业务已完成，只需修复投影。

## 18. 最后用五句话记住

1. 解散权限只认 `groups.owner_id`，不相信展示用的 `role=2`。
2. 墓碑写入、成员/群资料删除和协调事件写入必须在同一事务提交或回滚。
3. 500 人上限由群行锁内的 `COUNT + INSERT` 保证，不由前端按钮保证。
4. 历史消息只作为 MySQL 服务端审计留存，当前不向解散后的原成员开放。
5. MySQL 当前行与永久墓碑共同是真相，协调事件负责重试，Redis 可以重建，WebSocket 只是在线提示。
