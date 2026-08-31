# GoIM HTTP 接口层

在前端应用启动时创建一个 `GoIMApiClient`，再按领域创建 API：

```ts
import { GoIMApiClient, createAuthApi, createFriendsApi } from "./api";

const client = new GoIMApiClient({
  baseUrl: import.meta.env.VITE_API_BASE_URL,
  getAccessToken: () => localStorage.getItem("access_token"),
  onUnauthorized: () => {
    localStorage.removeItem("access_token");
    location.assign("/login");
  },
});

export const authApi = createAuthApi(client);
export const friendsApi = createFriendsApi(client);
```

每个方法直接返回响应 `data`；非 2xx、非零业务码或无效 JSON 会抛出 `ApiError`。调用方可根据 `error.status` 与 `error.code` 处理登录失效、参数错误和业务冲突。

上传头像使用 `createUploadApi(client).avatar(file)`，不要手动设置 `Content-Type`，浏览器会为 `FormData` 写入边界。
