# GoIM WebSocket 协议（v1）

本协议对应当前服务端实现与 `frontend/goim-ws-types.ts`。连接地址为 `ws://<host>/ws?token=<accessToken>`；令牌缺失或无效时握手返回 HTTP 401，前端应刷新令牌或回到登录页。

## 连接与心跳

- 同一用户只允许一个活跃连接。新连接建立后，旧连接先收到 `{ "type": "kick", "reason": "new_login" }`，随后关闭；该消息是唯一不带 `data` 的应用消息。
- 单条文本帧最大 4096 字节，服务端每 30 秒发送 WebSocket 原生 Ping 帧，60 秒未收到 Pong 即关闭连接。
- 浏览器 WebSocket 会自动响应原生 Ping；前端不需要也不应发送 JSON `ping`/等待 JSON `pong`。
- 连接断开后采用指数退避重连；重连成功立即发送 `syncReq`。若服务端发出 `kick`，停止重连并清除登录态。

除 `kick` 外，所有应用消息使用 `{"type":"...","data":{...}}`。未知 `type` 会被服务端忽略。

## 客户端 → 服务端

| type | data | 语义 |
| --- | --- | --- |
| `msg` | `{ msgId, convType, toId, msgType, content, timestamp }` | 发送私聊（`convType=1`）或群聊（`2`）；`msgId` 必须由客户端生成且在重试时复用，`timestamp` 为 Unix 毫秒。|
| `deliverAck` | `{ serverMsgId }` | 客户端收到消息的送达确认；当前仅记录日志，保持发送即可。|
| `readAck` | `{ convId }` | 标记私聊会话已读并清除未读数。|
| `syncReq` | `{ lastSyncTime, lastSyncMsgId?, batchSize }` | 拉取离线私聊及群聊消息；首次同步使用 `lastSyncTime: 0`、`lastSyncMsgId: 0`。后续分页同时回传上一批的 `syncTime` 和 `syncMsgId`。`batchSize` 省略或小于等于 0 时为 50。|
| `revokeMsg` | `{ convId, serverMsgId }` | 撤回消息；成功后收到 `msgRevoked`。|

## 服务端 → 客户端

| type | data | 前端动作 |
| --- | --- | --- |
| `serverAck` | `{ clientMsgId, serverMsgId, groupSeq?, timestamp }` | 将对应临时消息标记为已被服务端接收；收到该确认后不得重复发送。|
| `msg` | `InboxMessage` | 写入会话。私聊在线推送；群聊在线成员推送，离线时由 `syncReq` 获取。|
| `syncBatch` | `{ msgs, hasMore, syncTime, syncMsgId? }` | 合并消息并将 `syncTime + syncMsgId` 作为下一批复合游标；`hasMore=true` 时携带二者继续请求。|
| `convSync` | `{ conversations, unreadMap }` | 用服务端会话摘要和未读数覆盖或合并本地状态。|
| `msgRevoked` | `{ convId, serverMsgId, operatorId }` | 将对应消息替换为撤回占位状态。|
| `error` | `{ code, message }` | 将待发送消息标记失败；`4001~4003` 为私聊错误，`5001~5003` 为群聊错误。|
| `kick` | `{ type, reason }` | 清除令牌、关闭连接并跳转登录。|

`InboxMessage`：`{ msgId, convId, convType, fromId, toId, msgType, content, readStatus, groupSeq?, timestamp }`。会话 ID 规则：私聊 `p_{较小用户ID}_{较大用户ID}`，群聊 `g_{groupID}`。

## 推荐消息状态机

1. 发送前生成 UUID 作为 `msgId`，先插入 `pending` 本地消息。
2. 收到 `serverAck` 后，以 `clientMsgId` 关联，记录 `serverMsgId` 并改为 `sent`。
3. 断线前未获得 `serverAck` 的消息，重连后使用同一 `msgId` 重试；服务端重复校验会返回错误，前端据此去重。
4. 收到 `msg` 后写入本地、发送 `deliverAck`；用户查看该会话后发送 `readAck`。
5. 重连时先完成 `syncBatch` 分页，每页同时推进 `syncTime + syncMsgId` 复合游标，再处理 `convSync`；按 `msgId`/`serverMsgId` 去重。

## 未纳入 v1 的消息

`friendApply`、`friendAccepted`、`presence` 和应用层 `ping`/`pong` 尚无服务端业务实现，不属于前端 v1 契约。好友操作使用 HTTP API。
