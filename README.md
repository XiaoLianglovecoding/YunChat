# my_IM

这是基于本地开源项目 `E:\IT\IM` 重建的学习型 IM 项目。前端以逐文件复制为基线并统一了 MyIM 品牌；后端保留原项目的分层、数据模型、HTTP/WebSocket 契约与数据边界。工程基础设施以及账户、JWT 鉴权、刷新令牌轮换和头像链路已经实现，其余社交业务仍使用 TODO 占位。

## 当前状态

| 部分 | 状态 |
| --- | --- |
| React/Vite 前端 | 已复制 66 个源码、配置、测试和文档文件 |
| Go 服务启动器 | 已装配 MySQL、Redis、RabbitMQ，默认监听 `18080` |
| 健康检查 | `GET /health` 返回 200 |
| 业务 HTTP 契约 | 原 42 个路由全部注册 |
| WebSocket 入口 | `GET /ws` 已注册 |
| 账户与鉴权 | 注册、登录、刷新轮换、修改用户名/密码已实现 |
| 头像 | 安全上传、资料更新、公开读取与静态文件访问已实现 |
| 其余业务逻辑 | 保留 `TODO[任务编号]` 占位 |
| 受保护接口 | 全部经过真实 JWT 中间件；无效或缺失 Token 返回 401 |
| MySQL | 版本化迁移、连接池、事务 Repository；13 张上游表 + 1 张用户消息状态表 |
| Redis | 客户端、Repository、6 个单一来源 Lua、SHA 预加载与健康检查 |
| RabbitMQ | 4 个实际队列、DLQ、Confirm、mandatory、超时与有限重试 |
| 可观测性 | JSON 日志、Request ID、`/metrics`、本机 pprof |

`GET /ready` 会真实检查 MySQL、Redis、RabbitMQ，只有三者都可用才返回 200。注册成功使用统一响应信封：

```json
{
  "code": 1900,
  "message": "ok",
  "data": {"user_id": 1, "username": "alice"}
}
```

## 目录

```text
my_IM/
├── frontend/                    # 从 E:\IT\IM\frontend 原样复制
├── backend/
│   ├── cmd/server|migrate/      # 服务入口与迁移 up/status 命令
│   ├── configs/                 # 本地、示例、Docker 配置
│   ├── internal/
│   │   ├── api/                 # HTTP Handler、42 个业务路由与剩余 TODO
│   │   ├── auth|middleware/     # JWT 签发、校验和身份注入
│   │   ├── service/             # 账户/头像用例与后续业务接口
│   │   ├── repository/          # MySQL、Redis、MQ 端口
│   │   ├── model/               # 原数据模型
│   │   ├── protocol/            # WebSocket 信封协议
│   │   ├── ws|conn|consumer/    # 实时与异步模块边界
│   │   └── infra|middleware/    # 基础设施与中间件
│   └── scripts/
│       ├── migrations/          # 001..011 可升级迁移
│       └── baseline/            # 最终 14 表结构阅读基线
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

还可以执行 `Invoke-RestMethod 'http://localhost:18080/ready'`；三项依赖正常时返回 200。账户和头像端点可真实使用，好友、群组、消息、动态和设置端点通过鉴权后仍返回 501。

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

## 开发入口

- [业务开发任务清单](DEVELOPMENT_TASKS.md)：实施顺序、依赖和验收标准。
- [工程与基础设施小白教程](docs/FOUNDATION_TUTORIAL.md)：本阶段代码的逐层原理、命令与故障实验。
- [账户与鉴权小白教程](docs/AUTH_TUTORIAL.md)：从密码哈希、JWT、Redis 轮换到头像上传的完整讲解。
- [架构说明](docs/ARCHITECTURE.md)：模块边界、目标数据流和源码/文档漂移。
- [数据库与中间件契约](docs/DATABASE.md)：13 张表、Redis 键、MQ 队列和一致性风险。
- [前端复制与联调说明](docs/FRONTEND_COPY.md)：复制范围、环境变量和后端耦合点。
- [前端 HTTP 类型](frontend/goim-api-types.ts) 与 [WebSocket 类型](frontend/goim-ws-types.ts) 是跨端契约的主要入口。

建议严格按任务编号提交，例如 `feat(auth): complete AUTH-001 registration`。实现一个业务端点时，应同时删除对应路由 TODO、接入 Service/Repository、补测试并更新任务清单。

## 来源说明

前端、Go 模型和迁移文件来自本机当前检出的 `E:\IT\IM`；源仓库中的未提交改动没有被修改。审计时没有在源项目根目录发现 `LICENSE`、`COPYING` 或 `NOTICE`。此项目在公开发布或商业使用前，请自行确认上游许可证和复制授权。
