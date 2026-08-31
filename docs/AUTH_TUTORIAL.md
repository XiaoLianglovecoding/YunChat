# 账户与鉴权模块小白教程

本文对应 `DEVELOPMENT_TASKS.md` 的 `AUTH-001`～`AUTH-006`、`PROFILE-001` 和它依赖的 `UPLOAD-001`。读完后，你应该能说清楚一次登录经过哪些层、密码为什么不能明文保存、JWT 为什么还需要 Redis，以及一个看起来普通的头像上传为什么有这么多校验。

## 1. 先建立整体认识

这部分不是“写一个登录接口”这么简单，而是四类职责协作：

```text
浏览器
  │  JSON / multipart
  ▼
Handler（HTTP 层）
  │  把请求转换成 Command
  ▼
Service（业务层）
  ├── 密码规则、bcrypt、令牌策略、错误码
  ├── User Repository 接口 ──> MySQL
  ├── Refresh Repository 接口 ──> Redis
  └── Token Manager ──> JWT 签名/校验
```

每层只做自己的事：

- Handler 知道 HTTP、JSON、状态码，但不知道 SQL。
- Service 知道注册、登录、改密规则，但不知道 Gin 的 `Context`。
- Repository 知道怎样访问 MySQL/Redis，但不决定“密码至少 6 个字符”。
- Token Manager 只负责令牌，不查询用户。

这种拆分的直接好处是：Service 单元测试可以使用内存假仓库，不必每次都启动数据库；真实集成测试再替换成 MySQL 和 Redis 实现。

## 2. 本阶段最重要的文件

| 文件 | 作用 |
| --- | --- |
| `backend/internal/auth/token.go` | access/refresh JWT 的签发与严格校验 |
| `backend/internal/middleware/auth.go` | 读取 Bearer Token，把强类型用户 ID 注入请求 |
| `backend/internal/service/auth.go` | 注册、登录、刷新、改名、改密业务规则 |
| `backend/internal/service/avatar.go` | 头像资料读取与安全文件保存 |
| `backend/internal/api/auth_handler.go` | 账户 HTTP DTO 和响应 |
| `backend/internal/api/avatar_handler.go` | 头像公开读取和上传 HTTP 入口 |
| `backend/internal/repository/mysql_repository.go` | 用户定向 SQL 更新 |
| `backend/internal/repository/redis_extensions.go` | 刷新会话保存、轮换、按用户撤销 |
| `frontend/src/stores/authStore.ts` | 浏览器会话与刷新 Token 原子替换 |
| `backend/internal/api/auth_integration_test.go` | MySQL + Redis + HTTP 完整链路测试 |

读代码建议按表格顺序进行。先理解 Token，再看业务 Service，最后看 Handler 装配，思路会比从 `main.go` 一头扎进去清楚很多。

## 3. 注册：密码为什么必须哈希

注册请求是：

```json
{"username":"alice","password":"secret1"}
```

Service 做三件关键事情：

1. 用户名按 Unicode 字符计数，必须是 3～50 个字符，不能包含空白或控制字符。
2. 密码至少 6 个字符，并限制为 72 字节以内。
3. 用 bcrypt 生成哈希后才写入 `users.password_hash`。

bcrypt 的结果类似：

```text
$2a$10$......
```

它不是“可解密的密文”。登录时也不会把哈希还原成密码，而是让 bcrypt 判断“本次输入能不能产生匹配结果”。bcrypt 每次自动使用随机盐，所以两个用户使用相同密码，数据库里的哈希通常也不同。

为什么限制 72 字节？这是 bcrypt 的输入边界。中文 UTF-8 通常一个字占 3 字节，因此“字符数”和“字节数”是两个概念，代码分别处理。

### 重名为什么依赖数据库唯一键

不能只写：

```text
先 SELECT 看用户名不存在
再 INSERT
```

两个并发请求可能同时 SELECT 到“不存在”，然后一起 INSERT。真正的最后防线是 `users.username` 的 UNIQUE 约束。Repository 把 MySQL 1062 映射为 `repository.ErrConflict`，Service 再把它映射为业务码 1103。因此并发下仍然只有一个注册成功。

## 4. 登录：不泄露用户是否存在

常见的不安全写法是：

```text
用户不存在 -> 立即返回“用户不存在”
密码错误   -> 慢慢执行 bcrypt 后返回“密码错误”
```

攻击者既可以看错误文案，也可能比较响应时间，批量探测哪些用户名真实存在。

当前实现统一返回 1105：`wrong username or password`。当用户不存在时，Service 仍然拿预先生成的 dummy bcrypt 哈希做一次真实比较，使两条失败路径尽量接近。这里称为“恒定工作量”更准确；网络、数据库和操作系统调度仍会产生细小时间波动，不能理解成数学上每次耗时完全相同。

登录成功会拿到一对 Token：

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "expires_in": 7200
}
```

## 5. JWT：签名不是加密

JWT 由三段组成：

```text
header.payload.signature
```

payload 可以被浏览器解码，因此绝对不能放密码、密钥等秘密。签名的作用是防篡改：客户端能看到 `user_id`，但不能在不知道服务端密钥的情况下把它改成别人的 ID 并生成合法签名。

MyIM 的 Claims 包含：

- `user_id`：用户数字 ID。
- `username`：当前用户名。
- `token_type`：`access` 或 `refresh`。
- `iss`：签发方，例如 `my-im`。
- `sub`：用户 ID 的标准字符串形式。
- `jti`：当前令牌的唯一 ID。
- `iat`：签发时间。
- `exp`：过期时间。
- refresh 额外包含 `family_id`。

校验时不能只验证“能解析”。Token Manager同时限制：

- 算法必须精确为 HS256；拒绝 `none` 或其他算法。
- 签名必须匹配当前密钥。
- `iss` 必须匹配配置的签发方。
- `exp` 必须存在且未过期。
- `token_type` 必须符合使用场景，refresh 不能冒充 access。
- `user_id`、`username`、`jti`、`sub` 必须完整且互相一致。

配置要求 JWT 密钥至少 32 个字符。示例密钥只适合本地开发，生产环境应该用密码管理系统生成和注入随机密钥。

## 6. 为什么有 access 和 refresh 两种 Token

access token 用于每次业务请求，寿命较短；refresh token 只用于换取新的一对 Token，寿命较长。

这样做是在安全和体验间折中：

- access 很快过期，被窃取后的风险窗口较小。
- 用户不必每两小时重新输入密码，浏览器可用 refresh 自动续期。
- refresh 更敏感，因此服务端用 Redis 保存它的会话状态，使其可以撤销。

如果只有“完全无状态 JWT”，服务端签出去后通常只能等它自然过期。改密码、退出登录时就很难立即让长期令牌失效。

## 7. Refresh Token 一次性轮换

登录后，Redis 中会出现：

```text
refresh:{jti}          Hash：user_id、family_id、expires_at
refresh_user:{userID}  Set：这个用户的全部 jti
```

刷新过程是：

```text
客户端提交 refresh-v1
  -> 校验 JWT 签名/issuer/exp/type
  -> 生成 access-v2 + refresh-v2（family 不变、jti 改变）
  -> Redis WATCH refresh:{v1-jti}
  -> 事务删除 v1，写入 v2
  -> 返回 v2 令牌对
```

旧 Token 已经从 Redis 删除。再次提交 refresh-v1 会返回 1106。

为什么需要 `WATCH`？假设两个请求同时拿同一个 refresh-v1 来刷新，如果只是普通的“先读再删”，两边可能都成功。Redis 乐观事务会让其中一个提交成功，另一个因键已变化失败，并被映射成“旧令牌已使用”。单元测试用两个 goroutine 同时刷新，断言结果严格为一次成功、一次 1106。

`family_id` 表示一次登录连续轮换产生的令牌家族，`refresh_user:{uid}` 则用于“一键撤销这个用户全部登录会话”。修改用户名和密码都会使用这个索引。

### 前端为什么也必须替换 refresh token

后端已经作废 refresh-v1，如果浏览器只保存新的 access-v2，却继续保存 refresh-v1，那么下一次续期一定失败。

`authStore.rotateSession(response)` 使用一次 Zustand `set` 同时替换：

- access token
- refresh token
- access 过期时间
- JWT 中的新用户名
- 头像资料

这就是任务清单里“原子替换新 refresh_token”的含义。这里的原子是前端状态层的一次状态提交，不是数据库事务。

## 8. 鉴权中间件做什么

保护接口要求请求头：

```http
Authorization: Bearer <access_token>
```

中间件顺序如下：

1. 没有请求头：返回 HTTP 401、业务码 1003。
2. 不是严格的 Bearer 两段格式：返回 401、1106。
3. Token Manager 校验失败：返回 401、1106。
4. 成功：把 `middleware.UserID` 放进 Gin Context，再执行 Handler。

这里特意定义了 `type UserID int64`。如果所有模块都用字符串键配普通 `int64`，未来很容易把别的数字误当用户 ID。强类型不能解决所有权限问题，但能让错误更早暴露。

路由测试会遍历全部保护路由，逐个确认无 Token 都返回 401。以后新增保护路由时，应继续把它放在同一个 protected route group 中。

## 9. 修改用户名与密码

### 修改用户名

流程是：读取用户 → 定向更新 username → 撤销旧 refresh 会话 → 签发带新 username 的 TokenPair。

Repository 没有用“读整行再写整行”的 `UpdateUser`，而是提供定向 SQL：

```sql
UPDATE users
SET username=?, nickname=IF(nickname=?, ?, nickname)
WHERE id=?
```

这样并发修改头像时，不会被一个旧的 `avatar_url` 覆盖。数据库 UNIQUE 仍是并发重名的最终保证。

### 修改密码

流程是：验证当前密码 → 生成新哈希 → 撤销全部 refresh → 更新密码哈希 → 写安全审计日志。

撤销放在数据库更新前面是有意的：

- Redis 失败：接口失败，旧密码仍有效，不会留下未撤销的长期会话。
- Redis 成功、MySQL 失败：用户会被退出，但密码没变，可以重新登录。这是可恢复且更安全的失败方式。

审计日志只包含 `event=password_changed` 和 `user_id`，绝不记录当前密码、新密码或 Token。前端改密成功后清理本地会话并回到登录页。

注意：已签发的 access token 仍可工作到自身过期。这是当前“短 access + 可撤销 refresh”模型的边界。如果产品要求改密后连 access 也立即失效，可在后续引入用户 token version 或 access denylist。

## 10. 头像上传为什么不能只看后缀

攻击者完全可以把文本或程序改名成 `avatar.png`。因此上传服务同时检查：

- multipart 字段名必须是 `file`。
- 扩展名在配置允许列表中。
- 文件前 512 字节探测出的 MIME 必须和扩展名匹配。
- 内容大小不能超过配置上限（当前示例为 50 MiB）。
- 最终文件名由 `crypto/rand` 生成，不使用用户原文件名。
- `filepath.Rel` 确认目标没有逃出上传根目录。
- 先写同目录临时文件，`Sync`、`Close` 后再 `Rename` 到最终名。

为什么最后一步叫原子写？如果直接写最终文件，进程中途崩溃，静态服务器可能读到半张图片。临时文件完整落盘后，同文件系统的 Rename 会让最终路径从“不存在”一次切换到“完整文件”。

保存成功后数据库写入相对 URL：

```text
/uploads/avatars/<random>.png
```

前端 `VITE_STATIC_BASE_URL` 默认是 `http://localhost:18080`，两者拼起来正好是静态资源地址。`GET /api/v1/avatar/:userID` 对已上传头像返回 302 到这个 URL；未上传时返回用数据库用户名生成的 SVG 占位图。

当前 MIME sniff 满足本任务验收，但生产级图片系统通常还会真正解码并重新编码图片、剥离元数据、限制像素尺寸、做恶意内容扫描。这部分归入后续 `SEC-001`。

## 11. 自己运行一遍

### 启动基础设施

```powershell
Set-Location 'E:\IT\my_IM'
docker compose up -d mysql redis rabbitmq
docker ps --filter name=my-im
```

这三个服务运行在 Docker 中，Go 后端运行在本机；本机通过 `127.0.0.1:13306` 和 `127.0.0.1:16379` 访问 MySQL、Redis。

### 启动后端

```powershell
Set-Location 'E:\IT\my_IM\backend'
$env:GOCACHE = 'E:\IT\my_IM\.cache\go-build'
go run ./cmd/server -c configs/config.local.example.yaml
```

### 注册与登录

```powershell
$base = 'http://127.0.0.1:18080/api/v1'
$register = Invoke-RestMethod "$base/auth/register" -Method Post -ContentType 'application/json' -Body '{"username":"alice","password":"secret1"}'
$login = Invoke-RestMethod "$base/auth/login" -Method Post -ContentType 'application/json' -Body '{"username":"alice","password":"secret1"}'
$access = $login.data.access_token
$refresh = $login.data.refresh_token
```

不要把 `$access`、`$refresh` 打印到日志、聊天或截图里。

### 验证保护路由和刷新轮换

```powershell
$headers = @{ Authorization = "Bearer $access" }
Invoke-RestMethod "$base/friend/list" -Headers $headers

$refreshBody = @{ refresh_token = $refresh } | ConvertTo-Json
$rotated = Invoke-RestMethod "$base/auth/refresh" -Method Post -ContentType 'application/json' -Body $refreshBody

# 再用旧 $refresh 调一次，应得到 HTTP 401 / code 1106。
Invoke-RestMethod "$base/auth/refresh" -Method Post -ContentType 'application/json' -Body $refreshBody
```

好友业务尚未实现，所以带合法 access 请求 `/friend/list` 会到达 TODO 并返回 501；这反而证明 JWT 中间件已经放行了合法身份。无 Token 时应先返回 401。

## 12. 测试怎么分层

### 快速测试

```powershell
Set-Location 'E:\IT\my_IM\backend'
$env:GOCACHE = 'E:\IT\my_IM\.cache\go-build'
go test ./...

Set-Location 'E:\IT\my_IM\frontend'
npm.cmd run typecheck
npm.cmd test -- --run
npm.cmd run build
```

重要覆盖点：

- Token 测试：错误 issuer、签名、算法、过期和 token type。
- Middleware 测试：缺失/无效/refresh 冒充 access，以及强类型 ID。
- Service 测试：bcrypt、统一登录错误、并发 refresh 重放、改名和改密撤销。
- Avatar 测试：MIME 不匹配、超限、路径穿越、原子文件清理。
- 前端测试：刷新响应一次替换两个 Token。

### 真实 MySQL + Redis 集成测试

```powershell
Set-Location 'E:\IT\my_IM\backend'
$env:GOCACHE = 'E:\IT\my_IM\.cache\go-build'
$env:MYIM_INTEGRATION = '1'
go test ./internal/api -run TestAuthHTTPIntegration -v -count=1
Remove-Item Env:MYIM_INTEGRATION
```

这个测试创建唯一临时用户名，走完整 HTTP 链路，结束时按精确 user ID 删除测试行并撤销 Redis 会话，不会清空整张表或整个 Redis。

## 13. 常见问题

### 为什么登录成功，后续还是 401？

先检查请求是否使用 `access_token`，而不是 `refresh_token`；然后检查请求头是否为 `Authorization: Bearer ...`，Bearer 和 Token 中间要有空格。

### 为什么刷新第一次成功，第二次失败？

这是正确行为。refresh 是一次性的；前端必须保存第一次响应里的新 `refresh_token`。

### 为什么改密码后被送回登录页？

改密会撤销这个用户全部 refresh 会话。前端主动清理当前会话，要求使用新密码重新登录，避免保留一个无法继续刷新的半失效状态。

### 为什么数据库里看不到密码？

只能看到 bcrypt 哈希，这是设计目标。系统不应该有“查看用户原密码”的能力。忘记密码应设计重置流程，而不是解密旧密码。

### 为什么上传返回相对 URL？

相对 URL 不绑定域名。开发环境可拼 localhost，生产环境可拼 CDN 或正式域名，后端数据库不需要随部署环境修改。

## 14. 你可以做的练习

1. 给 `validateUsername` 增加表格驱动测试，覆盖中文、首尾空格、50/51 字符。
2. 在 Redis CLI 中观察登录前后 `refresh_user:{uid}` 的成员变化，但不要复制 Token。
3. 把 access 过期时间临时改短，观察前端只发起一次并发刷新。
4. 为头像增加像素尺寸限制：真正解码图片，拒绝超大宽高，再重新编码保存。
5. 设计登出接口：撤销当前 family 还是用户全部 family，并说明两者的产品差异。

完成这些练习后，你对鉴权的理解就不再只是“会调用 JWT 库”，而是能解释凭据存储、令牌生命周期、并发安全、失败顺序和跨端状态一致性。
