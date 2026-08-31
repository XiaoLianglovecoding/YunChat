# my_IM

这是基于本地开源项目 `E:\IT\IM` 重建的学习型 IM 工程骨架。前端已逐文件原样复制；后端保留原项目的分层、数据模型、HTTP/WebSocket 契约、MySQL 迁移、Redis/MQ 边界，但没有复制业务实现。

## 当前状态

| 部分 | 状态 |
| --- | --- |
| React/Vite 前端 | 已复制 66 个源码、配置、测试和文档文件 |
| Go 服务启动器 | 可启动，默认监听 `18080` |
| 健康检查 | `GET /health` 返回 200 |
| 业务 HTTP 契约 | 原 42 个路由全部注册 |
| WebSocket 入口 | `GET /ws` 已注册 |
| 业务逻辑 | 统一 `TODO[任务编号]` 占位 |
| 受保护接口 | 默认被 `AUTH-004` 中间件关闭，防止未鉴权误开放 |
| MySQL | 原 9 个迁移文件、13 张表完整保留 |
| Redis/RabbitMQ | 接口、键规范、队列名保留，实现待开发 |

`GET /ready` 当前故意返回 503；只有基础设施装配和关键业务完成后，才应改为真正的就绪检查。公开业务路由返回 HTTP 501 和前端认识的响应信封：

```json
{
  "code": 1900,
  "message": "TODO[AUTH-001]: 用户注册",
  "data": {"task_id": "AUTH-001", "feature": "用户注册"}
}
```

## 目录

```text
my_IM/
├── frontend/                    # 从 E:\IT\IM\frontend 原样复制
├── backend/
│   ├── cmd/server/              # 可运行的骨架入口
│   ├── configs/                 # 本地、示例、Docker 配置
│   ├── internal/
│   │   ├── api/                 # 42 个业务路由 + TODO 响应
│   │   ├── service/             # 用例接口与 TODO 编号
│   │   ├── repository/          # MySQL、Redis、MQ 端口
│   │   ├── model/               # 原数据模型
│   │   ├── protocol/            # WebSocket 信封协议
│   │   ├── ws|conn|consumer/    # 实时与异步模块边界
│   │   └── infra|middleware/    # 基础设施与中间件
│   └── scripts/
│       ├── migrations/          # 原始迁移，13 张表
│       └── lua/                 # 4 个明确失败的 TODO 脚本
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
Invoke-WebRequest 'http://localhost:18080/api/v1/auth/register' -Method Post
```

第二条请求返回 501 是骨架期的预期行为。

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
docker compose --profile skeleton-app up -d --build
```

该方式构建前端和后端，并由 Gin 托管 SPA；它仍是业务返回 501 的骨架服务。

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
- [架构说明](docs/ARCHITECTURE.md)：模块边界、目标数据流和源码/文档漂移。
- [数据库与中间件契约](docs/DATABASE.md)：13 张表、Redis 键、MQ 队列和一致性风险。
- [前端复制与联调说明](docs/FRONTEND_COPY.md)：复制范围、环境变量和后端耦合点。
- [前端 HTTP 类型](frontend/goim-api-types.ts) 与 [WebSocket 类型](frontend/goim-ws-types.ts) 是跨端契约的主要入口。

建议严格按任务编号提交，例如 `feat(auth): complete AUTH-001 registration`。实现一个业务端点时，应同时删除对应路由 TODO、接入 Service/Repository、补测试并更新任务清单。

## 来源说明

前端、Go 模型和迁移文件来自本机当前检出的 `E:\IT\IM`；源仓库中的未提交改动没有被修改。审计时没有在源项目根目录发现 `LICENSE`、`COPYING` 或 `NOTICE`。此项目在公开发布或商业使用前，请自行确认上游许可证和复制授权。
