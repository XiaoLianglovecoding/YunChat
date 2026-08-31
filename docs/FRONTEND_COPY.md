# 前端复制与联调说明

## 复制结果

`E:\IT\IM\frontend` 下的 66 个文件已完整复制到 `E:\IT\my_IM\frontend`，包括隐藏的 `.env.example`。没有复制 `node_modules`、`dist` 或本地环境文件；源目录当时也不存在这些生成物。

复制后没有修改前端源码，因此页面、Mock 预览、类型文件和测试仍保持原项目行为。必须保留前端根目录下的：

- `goim-api-types.ts`
- `goim-ws-types.ts`
- `package-lock.json`
- `vite.config.ts`、`vitest.config.ts`、`tsconfig.json`

`src` 内存在到两个根级类型文件的相对导入，只复制 `src` 会导致编译失败。

## 环境契约

`frontend/src/config/env.ts` 的默认值与新骨架一致：

```env
VITE_API_BASE_URL=http://localhost:18080/api/v1
VITE_WS_URL=ws://localhost:18080/ws
VITE_STATIC_BASE_URL=http://localhost:18080
```

开发服务器端口为 `5173`，没有 Vite proxy，因此后端必须处理跨域。`VITE_*` 是构建期变量，生产值变化后要重新构建。

## HTTP 耦合

- 成功响应必须是 `{code: 0, message, data}`。
- 非成功响应仍必须是 JSON，并包含 `code`、`message`。
- 鉴权头为 `Authorization: Bearer <token>`。
- 401 会触发刷新令牌并最多重试一次。
- 分页固定为 `{items,pagination:{total,offset,limit,has_more}}`。
- API 客户端超时为 15 秒。
- 头像上传表单字段名必须是 `file`。
- 头像常返回 `/uploads/...` 相对 URL，由 `VITE_STATIC_BASE_URL` 补全。

JWT 至少要带 `user_id`、`username`、`exp`。否则前端解析出的用户 ID 会退化为 0，导致私聊会话 ID 和权限判断错误。

## WebSocket 耦合

连接 URL 为 `/ws?token=<accessToken>`。客户端消息：

- `msg`
- `deliverAck`
- `readAck`
- `syncReq`
- `revokeMsg`

服务端主要消息：

- `msg`
- `serverAck`
- `syncBatch`
- `convSync`
- `msgRevoked`
- `kick`
- `groupAdded` / `groupRemoved`
- `error`

原 Go 协议还声明了 `friendApply`、`friendAccepted`、`presence`，但复制的 TypeScript `ServerWsMessage` 联合类型没有纳入它们；`FRIEND-005` 负责在实现前统一 Go 常量、TS 类型和前端处理器。

私聊会话 ID 固定为 `p_{较小用户ID}_{较大用户ID}`，群聊固定为 `g_{groupID}`。

## 已知前端事项

- `AuthPage.tsx` 和 `RouteGuards.tsx` 的默认聊天跳转包含 Mock 会话 `/app/chats/lin-cheng`；接真实后端时应改成无选中会话的入口。
- `messages.ts` 已导出，但目前没有在 `src/lib/api.ts` 实例化；消息搜索和个人删除尚未被页面使用。
- 生产构建开启 sourcemap，公开部署前决定是否关闭。
- 当前 UI 最小宽度约为 1080px，移动端适配需另立任务。
- 生产必须同时代理 `/api/v1`、`/ws`、`/uploads`，并为 BrowserRouter 配置 SPA fallback。

## 验证

```powershell
Set-Location 'E:\IT\my_IM\frontend'
Copy-Item -LiteralPath '.env.example' -Destination '.env.local'
npm.cmd ci
npm.cmd run typecheck
npm.cmd test
npm.cmd run build
```

Vite 8 要求 Node `^20.19.0 || >=22.12.0`；当前本机 Node 24 满足要求。
