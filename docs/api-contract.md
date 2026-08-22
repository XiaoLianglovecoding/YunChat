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

业务错误使用 4xx，系统错误使用 5xx；不会把 HTTP 200 当作所有业务结果的容器。M1 身份与账号端点已实现，其余里程碑端点仍返回 HTTP 501 和 `NOT_IMPLEMENTED`。

## REST 资源

| 方法 | 路径 | 用途 | 状态 |
| --- | --- | --- | --- |
| POST | `/auth/register` | 注册 | 已实现 |
| POST | `/auth/login` | 登录 | 已实现 |
| POST | `/auth/refresh` | 轮换令牌 | 已实现 |
| POST | `/auth/logout` | 注销当前设备 | 已实现 |
| POST | `/auth/change-password` | 修改密码并吊销会话 | 已实现 |
| GET/PATCH | `/users/me` | 当前用户资料 | 已实现 |
| GET/PATCH | `/users/me/settings` | 当前用户设置 | 已实现 |
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

### M1 认证约定

- 注册和登录请求支持 `deviceKey`、`deviceName`、`platform`，服务端当前采用单设备策略：同一用户的新登录会吊销旧设备会话。
- `refreshToken` 为一次性轮换令牌。刷新成功后旧令牌立即失效；检测到令牌重放时会吊销整个令牌族。
- 访问令牌中的 `session_id` 会在每次鉴权时校验数据库会话状态，因此注销、改密和刷新后的旧访问令牌都会失效。
- 登录和注册同时受 Redis IP 限流及账号维度登录失败限流保护。

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
