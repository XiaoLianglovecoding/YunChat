# 群聊第三步教程：管理员角色与成员禁言

这篇教程对应开发清单中的 `GROUP-003`。它建立在已经完成的 `GROUP-001` 群资料和 `GROUP-002` 成员管理之上。

本阶段解决两件事：

1. 群主可以把普通成员设为管理员，也可以取消管理员；
2. 群主或管理员可以在权限范围内禁言、解禁成员，群消息 Lua 会真实拒绝仍在禁言期内的成员。

你会在这篇教程里学到：权限矩阵怎样翻译成代码、为什么角色和禁言也要使用事务、为什么 Redis Hash 必须保存完整成员信息，以及为什么判断禁言到期应使用 Redis 自己的时间。

## 1. 本阶段的 HTTP 接口

所有接口都需要 access token。

| 功能 | 方法与路径 | 请求体 | 成功结果 |
| --- | --- | --- | --- |
| 设置/取消管理员 | `PUT /api/v1/group/:groupID/member/:memberID/role` | `{"role": 1}` 或 `{"role": 0}` | HTTP 200，省略 `data` |
| 禁言成员 | `PUT /api/v1/group/:groupID/member/:memberID/mute` | `{"muted_until":"2099-09-07T15:00:00Z"}` | HTTP 200，省略 `data` |
| 解除禁言 | `DELETE /api/v1/group/:groupID/member/:memberID/mute` | 无 | HTTP 200，省略 `data` |

这里的 `memberID` 是用户 ID，不是 `group_members.id`。

解禁使用 DELETE，而不是 PUT 一个 `null`，是为了让契约没有歧义：PUT 必须带一个有效的未来截止时间；DELETE 明确表示删除禁言状态。这样 `{}`、字段缺失和“我要解禁”不会被误认为同一件事。

## 2. 数据库其实早已准备好

不需要新增表。`group_members` 已经同时保存角色和禁言截止时间：

```text
group_members
  group_id | user_id | role | muted_until
  100      | 7       | 2    | NULL
  100      | 8       | 1    | NULL
  100      | 9       | 0    | 2099-09-07 15:00:00
```

角色常量：

```text
0 = 普通成员
1 = 管理员
2 = 群主
```

`muted_until` 的含义：

- `NULL`：没有禁言；
- 晚于当前时间：仍在禁言；
- 早于或等于当前时间：禁言已经自然到期。

数据库保存“截止时间”，而不是只保存 `muted=true`。如果只保存一个布尔值，时间到了还必须依赖定时任务逐个解禁；保存截止时间后，每次判断时比较当前时间即可自然过期。

## 3. 权限必须先写成表

### 3.1 任免管理员

| 操作者 | 修改普通成员 | 修改管理员 | 修改真实群主 |
| --- | --- | --- | --- |
| 真实群主 | 可以设为管理员 | 可以取消管理员 | 不可以 |
| 管理员 | 不可以 | 不可以 | 不可以 |
| 普通成员/非成员 | 不可以 | 不可以 | 不可以 |

任免管理员只属于群主。管理员不能继续任命新的管理员，也不能取消同级，否则管理员数量和权限会失去群主控制。

请求中的角色只允许 `0` 和 `1`。客户端不能通过传入 `2` 把任何人变成群主；转让群主必须同时维护 `groups.owner_id` 和两条成员角色，属于独立的 `GROUP-004` 事务。

### 3.2 禁言与解禁

| 操作者 | 目标是普通成员 | 目标是管理员 | 目标是真实群主 |
| --- | --- | --- | --- |
| 真实群主 | 可以 | 可以 | 不可以 |
| 管理员 | 可以 | 不可以 | 不可以 |
| 普通成员/非成员 | 不可以 | 不可以 | 不可以 |

同一套矩阵同时用于禁言和解禁。否则会出现“管理员不能禁言同级，却能解除群主设置的同级禁言”这类越权漏洞。

当前用户也不能通过管理接口操作自己：群主不能禁言真实群主，管理员不能管理管理员同级。以后如果需要“自我禁言”之类的产品功能，应设计成另一条语义明确的业务，而不是绕过权限表。

所有“无权管理这个目标”的情况统一返回 `1301`，避免把 GROUP-002 专用的“不能移除群主/同级”错误文案错误套用到禁言或角色修改。

## 4. 为什么真实群主必须看 owner_id

正确判断是：

```text
group.owner_id == operatorID
```

不能只判断：

```text
operatorMember.role == 2
```

原因是数据库当前没有一个简单唯一索引能保证每群绝不出现第二条异常 `role=2`。如果人工改库或旧版本 Bug 制造了一条额外 `role=2`，盲信角色字段就会把攻击者当成群主。

因此：

- `groups.owner_id` 是真实群主身份；
- `group_members.role` 用于列表展示、管理员权限与缓存投影；
- 两者发生异常冲突时，权限判断优先信任 `owner_id`。

## 5. 角色修改的事务流程

业务入口位于 [group.go](../backend/internal/service/group.go)，数据库方法位于 [group_repository.go](../backend/internal/repository/group_repository.go)。

```text
BEGIN
  1. 锁定 groups 行，确认群存在
  2. 锁定操作者成员行，确认操作者仍在群内且是真实群主
  3. 锁定目标成员行，确认目标仍在群内
  4. 拒绝修改真实群主
  5. UPDATE group_members SET role = 0/1
  6. INSERT cache_reconcile_events(group_members, groupID)
COMMIT
  7. 尽力立即按 MySQL 完整重建 Redis 成员投影
```

为什么还要锁群行？GROUP-002 的添加、移除，以及未来 GROUP-004 的转让/退群都会先锁同一条群记录。所有群成员写操作采用相同锁顺序：

```text
groups → operator member → target member
```

统一顺序既让同群操作形成确定先后，也降低不同事务反向等待造成死锁的概率。

重复把管理员设为管理员，或者重复把普通成员设为普通成员，都按成功处理。这叫幂等：客户端因网络超时重试时，不会因为目标已经是期望状态而得到假失败。

## 6. 禁言与解禁的事务流程

禁言和解禁共用同一个 Service 方法：非 nil 截止时间表示禁言，nil 表示解禁。

```text
BEGIN
  1. 锁定 groups 行，确认群存在
  2. 锁定操作者成员行，确认群主/管理员身份
  3. 锁定目标成员行，确认目标存在
  4. 按“群主可管管理员/成员，管理员只可管成员”检查权限
  5. UPDATE group_members SET muted_until = ? / NULL
  6. INSERT cache_reconcile_events(group_members, groupID)
COMMIT
  7. 尽力立即重建 Redis
```

PUT 请求的截止时间必须：

- 是合法 RFC3339，例如 `2099-09-07T15:00:00+08:00`；
- 晚于服务端当前时间；
- 在进入持久化前转成 UTC，并截到 MySQL `DATETIME` 的秒精度。

过期时间不能偷偷当作解禁。调用者若要解禁，应明确发送 DELETE，这能减少错误客户端造成的意外操作。

为什么要截到秒？浏览器的 `toISOString()` 通常带毫秒，但当前数据库列不保存小数秒。如果直接拿请求值和数据库值比较，同一个请求重试时也可能被误判为“又变了一次”。先按数据库精度归一化，既避免临界时间落库后已经到期，也让 PUT 真正幂等。

应用服务、浏览器和 Redis 是三台不同的时钟。生产环境应使用 NTP 保持校时，客户端也不要提交只比当前时间晚几毫秒的截止值；当前界面最短提供 10 分钟，从而留出网络和时钟偏差余量。最终发消息能否通过，以 Lua 执行时的 Redis `TIME` 为准。

重复解禁一个本来就没有禁言的成员也算成功，仍符合幂等原则。

## 7. 为什么必须写“完整”的 Redis 成员信息

群消息 Lua 读取：

```text
group_members:{gid}       Set：成员身份
group_member_info:{gid}   Hash：角色和禁言截止时间
```

Hash 中每个 field 是用户 ID，value 是 JSON。例如：

```json
{"role":1,"muted_until":1788764400000}
```

没有禁言时：

```json
{"role":1}
```

更新角色时不能只在 Redis 改 `role` 而丢掉 `muted_until`；解禁时也不能删除整个 Hash field，否则连角色都没了。正确方式是重新读取 MySQL 的完整成员快照，再一次性重建：

- `group_members:{gid}`；
- `group_member_info:{gid}`；
- 每个成员的 `user_groups:{uid}`；
- owner index 与 loaded marker。

因此下面三个连续操作不会互相覆盖：

```text
普通成员禁言 → {role:0, muted_until:...}
晋升管理员   → {role:1, muted_until:...}
解除禁言     → {role:1}
```

## 8. MySQL 事务与缓存协调事件

角色/禁言 UPDATE 和 `cache_reconcile_events` 在同一个 MySQL 事务中：

```text
UPDATE 成功，事件 INSERT 失败 → ROLLBACK，两者都不生效
UPDATE 成功，事件 INSERT 成功 → COMMIT，一定有后台修复凭据
```

提交后 Service 立即调用 `ReconcileGroupMembers`。如果 Redis 临时失败，HTTP 仍然反映已经提交的 MySQL 事实；后台 Worker 会根据事件重试。

生产路径不调用旧的 Redis 增量角色/禁言接口，因为延迟请求可能覆盖更新后的状态。协调事件表达的是“重新读取当前真相”，不是“重放某次旧修改”，所以重复、延迟和乱序都不会把旧角色或旧禁言复活。

## 9. 群消息 Lua 怎样判断禁言

Lua 的核心逻辑可以简化成：

```text
确认 sender 在 group_members Set
读取 group_member_info Hash 中 sender 的 JSON
用 Redis TIME 得到当前 Unix 毫秒
若 muted_until > Redis 当前时间：返回群禁言错误 5002
否则：继续去重、分配消息 ID 和 group_seq
```

为什么不用应用服务器的 `time.Now()`？假设有三台应用服务器，其中一台时钟快 5 秒、另一台慢 5 秒，同一个成员可能在不同机器上得到不同结果。Lua 使用执行脚本的 Redis 服务器时间，所有应用实例共享同一把时钟。

截止时间到达后，不需要重写 Redis：同一个数值与不断前进的 Redis TIME 比较，结果会自动从“禁止”变成“允许”。数据库里保留已经过去的截止时间也不会继续禁言。

当 Lua 返回禁言时，它会在消息去重、消息 ID 和群序号分配之前退出。因此被拒绝的发送不会消耗 dedup key 或 group sequence。

## 10. Handler、Service、Repository 的分工

### Handler

[group_handler.go](../backend/internal/api/group_handler.go) 负责：

- 从 JWT Context 读取操作者 ID；
- 校验 `groupID/memberID`；
- 绑定 role 或 muted_until；
- 把 RFC3339 转成 Go 时间；
- 输出统一 HTTP 信封。

### Service

[group.go](../backend/internal/service/group.go) 负责：

- 角色值和时间的业务校验；
- 真实群主与目标权限矩阵；
- 事务顺序；
- 错误码；
- 提交后缓存重建。

### Repository

[group_repository.go](../backend/internal/repository/group_repository.go) 负责：

- 把所有调用绑定到同一个 `sql.Tx`；
- `SELECT ... FOR UPDATE`；
- 更新 `role/muted_until`；
- 写协调事件。

不要把权限只写在 Handler。未来后台任务或 WebSocket 命令可能直接调用 Service；只有规则放在 Service，所有入口才会得到同一套保护。

## 11. 前端怎样使用

群管理抽屉会直接使用 GROUP-002 已经收齐的完整成员列表。

角色按钮：

- 只对真实群主显示；
- 普通成员显示“设为管理员”；
- 管理员显示“取消管理员”；
- 真实群主自己没有按钮。
- 点击后先显示二次确认，确认后才提交权限变更。

禁言按钮：

- 群主能对管理员和普通成员使用；
- 管理员只能对普通成员使用；
- 提供 10 分钟、1 小时和 24 小时截止时间；
- 正在禁言的成员显示截止时间和“解除禁言”；
- 请求进行中禁用按钮，防止双击重复提交。

操作成功后失效共享的 `group-members` Query，聊天页和管理抽屉都会重新读取包含最新 `role/muted_until` 的完整成员资料。

前端隐藏按钮只是用户体验，不是安全措施。攻击者可以手工构造 HTTP，所以后端事务里的权限检查永远不能省略。

## 12. 错误码速查

| code | HTTP | 含义 |
| --- | --- | --- |
| `1002` | 400 | ID、JSON、RFC3339 时间或截止时间非法 |
| `1301` | 403 | 无权对这个目标修改角色或禁言 |
| `1302` | 404 | 群不存在 |
| `1307` | 400 | 目标角色不是 0/1 |
| `1310` | 404 | 目标成员不存在 |
| `5002` | 403 | 群消息发送者仍在禁言期 |

## 13. 用 PowerShell 自己调用

先准备群主 access token：

```powershell
$base = 'http://localhost:18080/api/v1'
$groupID = 100
$memberID = 9
$ownerToken = '替换为群主 access_token'
$headers = @{ Authorization = "Bearer $ownerToken" }
```

设为管理员：

```powershell
Invoke-RestMethod -Method Put `
  -Uri "$base/group/$groupID/member/$memberID/role" `
  -Headers $headers -ContentType 'application/json' `
  -Body '{"role":1}'
```

取消管理员：

```powershell
Invoke-RestMethod -Method Put `
  -Uri "$base/group/$groupID/member/$memberID/role" `
  -Headers $headers -ContentType 'application/json' `
  -Body '{"role":0}'
```

禁言 1 小时：

```powershell
$deadline = (Get-Date).ToUniversalTime().AddHours(1).ToString('o')
$body = @{ muted_until = $deadline } | ConvertTo-Json
Invoke-RestMethod -Method Put `
  -Uri "$base/group/$groupID/member/$memberID/mute" `
  -Headers $headers -ContentType 'application/json' -Body $body
```

解除禁言：

```powershell
Invoke-RestMethod -Method Delete `
  -Uri "$base/group/$groupID/member/$memberID/mute" `
  -Headers $headers
```

查看结果：

```powershell
Invoke-RestMethod -Method Get `
  -Uri "$base/group/$groupID/members?limit=100&offset=0" `
  -Headers $headers
```

## 14. 检查 MySQL 与 Redis

MySQL：

```sql
SELECT group_id, user_id, role, muted_until
FROM group_members
WHERE group_id = 100 AND user_id = 9;

SELECT resource_type, resource_id, status, attempts, last_error
FROM cache_reconcile_events
WHERE resource_type = 'group_members' AND resource_id = 100
ORDER BY id DESC;
```

Redis：

```text
HGET group_member_info:100 9
SISMEMBER group_members:100 9
SISMEMBER user_groups:9 100
GET group_member_loaded:100
```

角色修改只能改变 JSON 中的 role；禁言只能增加/改变 muted_until；解禁后 muted_until 消失，但 role 必须保留。

## 15. 测试在证明什么

- Service 单元测试：真实群主判断、任免限制、禁言权限矩阵、非法角色/时间、幂等和事务回滚。
- Handler/Router 测试：JWT 操作者透传、缺失字段、RFC3339、三条真实路由，以及 GROUP-004 仍返回 501。
- MySQL + Redis 集成测试：角色与禁言跨字段不丢失、协调事件、完整 Hash 投影。
- Lua 集成测试：禁言返回 5002；到期或解禁后允许；拒绝时不分配 dedup key 和 group_seq。
- 前端测试：权限按钮、三个时长、禁言展示、解禁、中文错误与防重复提交。

验证命令：

```powershell
Set-Location 'E:\IT\my_IM\backend'
go test ./...
go vet ./...

$env:MYIM_INTEGRATION = '1'
go test ./internal/service ./internal/redis -run 'Group(Role|Mute|Moderation)' -count=1 -v

Set-Location 'E:\IT\my_IM\frontend'
npm.cmd test
npm.cmd run build
```

## 16. 最后用三句话记住

1. 身份看真实 `owner_id`，能力看明确权限矩阵，不能相信前端按钮。
2. MySQL 的角色/禁言和修复事件一起提交；Redis 每次从完整真相重建，不能只补一个字段。
3. 禁言保存截止时间，Lua 用 Redis TIME 比较，所以自然到期且所有应用实例判断一致。
