# API 与实时协议

所有 HTTP 端点以 `/api/v1` 开头。成功和失败都使用稳定 envelope：

```json
{
  "code": "OK",
  "message": "success",
  "data": {},
  "request_id": "01J..."
}
```

业务错误使用 4xx，系统错误使用 5xx；不会把 HTTP 200 当作所有业务结果的容器。骨架阶段业务端点统一返回 HTTP 501 和 `NOT_IMPLEMENTED`。

## REST 资源

| 方法 | 路径 | 用途 | 状态 |
| --- | --- | --- | --- |
| POST | `/auth/register` | 注册 | TODO |
| POST | `/auth/login` | 登录 | TODO |
| POST | `/auth/refresh` | 轮换令牌 | TODO |
| POST | `/auth/logout` | 注销设备 | TODO |
| GET/PATCH | `/users/me` | 当前用户资料 | TODO |
| GET | `/users/:id` | 用户公开资料 | TODO |
| GET/POST | `/friend-requests` | 查询/发起好友申请 | TODO |
| POST | `/friend-requests/:id/accept` | 同意申请 | TODO |
| POST | `/friend-requests/:id/reject` | 拒绝申请 | TODO |
| GET/DELETE | `/contacts`、`/contacts/:id` | 联系人管理 | TODO |
| PUT/DELETE | `/blocks/:user_id` | 拉黑管理 | TODO |
| GET/POST | `/conversations` | 会话列表/创建会话 | TODO |
| GET | `/conversations/:id/messages` | 游标分页消息 | TODO |
| POST | `/conversations/:id/read` | 更新已读游标 | TODO |
| POST/PATCH | `/groups`、`/groups/:id` | 群组管理 | TODO |
| GET/POST/DELETE | `/groups/:id/members` | 群成员管理 | TODO |
| POST | `/uploads/presign` | 申请上传凭证 | TODO |

分页统一使用 `cursor` 与 `limit`，返回 `next_cursor`，不使用页码扫描历史消息。

## WebSocket Envelope

客户端发送：

```json
{
  "id": "client-event-id",
  "type": "message.send",
  "timestamp": 1787241600000,
  "data": {}
}
```

服务端响应或推送保持同一 envelope。`type` 初始集合：

| 类型 | 方向 | 说明 |
| --- | --- | --- |
| `system.hello` | S → C | 连接建立与协议版本 |
| `system.ping` / `system.pong` | 双向 | 心跳 |
| `message.send` | C → S | 发送消息命令 |
| `message.ack` | S → C | 已持久化确认，包含服务端 ID 和 seq |
| `message.created` | S → C | 新消息推送 |
| `message.recalled` | S → C | 撤回通知 |
| `conversation.read` | 双向 | 已读游标更新 |
| `typing.changed` | 双向 | 短期输入状态，不持久化 |
| `error` | S → C | 协议或业务错误 |

协议版本通过 `system.hello.data.protocol_version` 协商。增加字段必须向后兼容；破坏性变更升级主版本。

