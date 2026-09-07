# my_IM

这是基于本地开源项目 `E:\IT\IM` 重建的学习型 IM 项目。前端以逐文件复制为基线并统一了 MyIM 品牌；后端保留原项目的分层、数据模型、HTTP/WebSocket 契约与数据边界。工程基础设施、账户鉴权、好友关系以及关系缓存真相链路已经实现，其余业务仍使用 TODO 占位。

## 当前状态

| 部分 | 状态 |
| --- | --- |
| React/Vite 前端 | 已复制 66 个源码、配置、测试和文档文件 |
| Go 服务启动器 | 已装配 MySQL、Redis、RabbitMQ，默认监听 `18080` |
| 健康检查 | `GET /health` 返回 200 |
| 业务 HTTP 契约 | 原 42 个路由全部注册 |
| WebSocket 入口 | 已实现 JWT 握手、单用户连接替换、心跳租约及单实例好友/在线事件；跨实例 fanout 与聊天帧分派待后续任务 |
| 账户与鉴权 | 注册、登录、刷新轮换、修改用户名/密码已实现 |
| 头像 | 安全上传、资料更新、公开读取与静态文件访问已实现 |
| 好友 | 申请/分页、接受/拒绝、好友列表/删除、拉黑/解除及实时刷新已实现 |
| 缓存真相 | MySQL 真相、启动预热、按需回源、事务协调事件、后台修复及 `cachectl` 巡检已实现 |
| 其余业务逻辑 | 群管理、聊天消息、朋友圈和设置仍保留 `TODO[任务编号]` 占位 |
| 受保护接口 | 全部经过真实 JWT 中间件；无效或缺失 Token 返回 401 |
| MySQL | 版本化迁移、连接池、事务 Repository；13 张上游表 + 用户消息状态表 + 缓存协调事件表 |
| Redis | 6 个单一来源 Lua；好友/黑名单/群成员投影可从 MySQL 重建；显式 noeviction；在线租约带连接所有权 |
| RabbitMQ | 4 个实际队列、DLQ、Confirm、mandatory、超时与有限重试 |
| 可观测性 | JSON 日志、Request ID、`/metrics`、本机 pprof |

`GET /ready` 会真实检查 MySQL、Redis、RabbitMQ，只有三者都可用才返回 200。注册成功使用统一响应信封：

```json
{
  "code": 0,
  "message": "ok",
  "data": {"user_id": 1, "username": "alice"}
}
```

## 目录

```text
my_IM/
├── frontend/                    # 从 E:\IT\IM\frontend 原样复制
├── backend/
│   ├── cmd/server|migrate|cachectl/ # 服务、迁移与缓存重建/巡检命令
│   ├── configs/                 # 本地、示例、Docker 配置
│   ├── internal/
│   │   ├── api/                 # HTTP Handler、42 个业务路由与剩余 TODO
│   │   ├── auth|middleware/     # JWT 签发、校验和身份注入
│   │   ├── service/             # 账户、头像、好友与缓存真相用例
│   │   ├── repository/          # MySQL、Redis、MQ 端口
│   │   ├── model/               # 原数据模型
│   │   ├── protocol/            # WebSocket 信封协议
│   │   ├── ws|conn|consumer/    # 实时与异步模块边界
│   │   └── infra|middleware/    # 基础设施与中间件
│   └── scripts/
│       ├── migrations/          # 001..012 可升级迁移
│       └── baseline/            # 最终 15 张业务/协调表结构阅读基线
├── docs/
├── DEVELOPMENT_TASKS.md         # 按依赖顺序拆分的业务任务
├── docker-compose.yaml          # MySQL、Redis、RabbitMQ
└── Dockerfile                   # 前后端多阶段构建
```

## 快速启动

### 1. 启动依赖

```powershell
Set-Location 'E:\IT\my_IM'
Copy-Item -LiteralPath '.env.example' -Destination '.env'
docker compose up -d
```

MySQL、Redis、RabbitMQ 仅对宿主机 `127.0.0.1` 暴露 `13306`、`16379`、`5673`，RabbitMQ 管理页为 `http://localhost:15673`。

### 2. 启动后端骨架

```powershell
Set-Location 'E:\IT\my_IM\backend'
Copy-Item -LiteralPath 'configs\config.local.example.yaml' -Destination 'configs\config.local.yaml'
$env:GOCACHE = 'E:\IT\my_IM\.cache\go-build'
go run ./cmd/server -c configs/config.local.yaml
```

若修改了 `.env` 中的 MySQL/RabbitMQ 本地凭据，也要同步修改 `config.local.yaml`；一体化容器方式会自动把同一组 Compose 变量传给后端。

验证：

```powershell
Invoke-RestMethod 'http://localhost:18080/health'
Invoke-RestMethod 'http://localhost:18080/api/v1/auth/register' -Method Post -ContentType 'application/json' -Body '{"username":"alice","password":"secret1"}'
```

还可以执行 `Invoke-RestMethod 'http://localhost:18080/ready'`；三项依赖正常时返回 200。账户、头像和好友端点可真实使用；群组、消息、动态和设置端点通过鉴权后仍返回 501。

### 3. 启动前端

当前机器 PowerShell 会拦截 `npm.ps1`，请使用 `npm.cmd`：

```powershell
Set-Location 'E:\IT\my_IM\frontend'
Copy-Item -LiteralPath '.env.example' -Destination '.env.local'
npm.cmd ci
npm.cmd run dev
```

访问 `http://localhost:5173`。业务后端尚未实现时，可在开发环境登录页使用“暂不连接后端，进入界面预览”。

### 4. 使用一体化容器（与第 2、3 步二选一）

```powershell
Set-Location 'E:\IT\my_IM'
docker compose --profile full-stack up -d --build
```

该方式构建前端和后端，并由 Gin 托管 SPA。

### 5. 一键代码校验

首次执行或依赖变化时加 `-InstallFrontend`：

```powershell
Set-Location 'E:\IT\my_IM'
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify.ps1 -InstallFrontend
```

这里的 `Bypass` 只作用于本次子进程，不修改系统的 PowerShell 执行策略。

脚本校验 Compose 静态配置、Go 测试以及前端 typecheck/test/build；它不代替真实容器、数据库迁移和故障恢复 smoke test。

### 6. 重建或巡检关系缓存

在 `backend` 目录执行：

```powershell
go run ./cmd/cachectl -c configs/config.local.yaml -action rebuild -scope all
go run ./cmd/cachectl -c configs/config.local.yaml -action audit -scope all
```

`scope` 还可选 `friends`、`blacklists`、`groups`。`audit` 发现不一致时会以退出码 2 结束，适合接入运维脚本；它不会把 Redis 反向写回 MySQL。显式 `rebuild` 是严格修复路径，会在每个资源锁内执行增量 SCAN 以清理 owner index 外的人工孤儿，因此应作为运维命令使用；应用启动预热和在线修复仍走有界索引。

当前 Compose 只有一个 `app` 实例，好友 WebSocket 事件也按单实例实现。以后横向扩容时，需先在 WS-001/OPS 中加入 Redis Pub/Sub 或 RabbitMQ 跨实例 fanout；好友申请和好友关系本身已经持久化在 MySQL，不依赖事件帧保存。

## 开发入口

- [业务开发任务清单](DEVELOPMENT_TASKS.md)：实施顺序、依赖和验收标准。
- [工程与基础设施小白教程](docs/FOUNDATION_TUTORIAL.md)：本阶段代码的逐层原理、命令与故障实验。
- [账户与鉴权小白教程](docs/AUTH_TUTORIAL.md)：从密码哈希、JWT、Redis 轮换到头像上传的完整讲解。
- [好友与缓存真相小白教程](docs/FRIEND_CACHE_TUTORIAL.md)：从并发申请、双向好友事务到 Redis 回源、Outbox 修复和实时事件。
- [群资料小白教程](docs/GROUP_TUTORIAL.md)：从建群事务、成员关系到资料查询与更新。
- [群成员管理小白教程](docs/GROUP_MEMBER_TUTORIAL.md)：好友邀请、移除权限、并发容量、分页与 Redis 正反向一致性。
- [架构说明](docs/ARCHITECTURE.md)：模块边界、目标数据流和源码/文档漂移。
- [数据库与中间件契约](docs/DATABASE.md)：13 张表、Redis 键、MQ 队列和一致性风险。
- [前端复制与联调说明](docs/FRONTEND_COPY.md)：复制范围、环境变量和后端耦合点。
- [前端 HTTP 类型](frontend/goim-api-types.ts) 与 [WebSocket 类型](frontend/goim-ws-types.ts) 是跨端契约的主要入口。

建议严格按任务编号提交，例如 `feat(auth): complete AUTH-001 registration`。实现一个业务端点时，应同时删除对应路由 TODO、接入 Service/Repository、补测试并更新任务清单。

## 来源说明

前端、Go 模型和迁移文件来自本机当前检出的 `E:\IT\IM`；源仓库中的未提交改动没有被修改。审计时没有在源项目根目录发现 `LICENSE`、`COPYING` 或 `NOTICE`。此项目在公开发布或商业使用前，请自行确认上游许可证和复制授权。
