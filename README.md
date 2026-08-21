<div align="center">

# LinkNest IM

面向个人开发者维护的即时通讯系统骨架。

`Go` · `React` · `TypeScript` · `WebSocket` · `MySQL` · `Redis` · `RabbitMQ`

</div>

> 当前阶段是 **architecture-first scaffold**：工程边界、协议、公共工具、数据表和部署入口已建立，聊天业务逻辑使用 `TODO` 或 `501 Not Implemented` 明确占位。

## 为什么这样设计

LinkNest IM 采用模块化单体，而不是一开始拆成大量微服务。HTTP API、WebSocket Gateway 共用一个进程，异步任务由独立 Worker 承担；当流量增长时，可以沿既有端口和仓储接口拆分。

- 单聊与群聊统一为 `conversation`，消息只走一套主链路。
- MySQL 是事实数据源，Redis 只保存在线状态、限流计数与热点数据。
- 本地事务同时写业务表和 `outbox_events`，Worker 再可靠投递 RabbitMQ。
- `client_message_id + sender_id` 保证客户端重试幂等，`conversation_seq` 保证会话内有序。
- 领域、应用、适配器分层，业务代码不会绑定 Gin、Redis 或 RabbitMQ。
- 前端按 feature 划分，API 与 WebSocket 协议集中管理。

```text
Browser
  |-- REST ----------------------> API / application / domain
  |-- WebSocket -----------------> realtime hub
                                        |
                   MySQL <--- repositories + transaction ---> outbox
                     |                                      |
                   Redis                              RabbitMQ <--- Worker
```

详细说明见 [系统架构](docs/architecture.md)、[数据模型](docs/database.md) 和 [API 契约](docs/api-contract.md)。

## 项目结构

```text
linknest-im/
├── backend/
│   ├── cmd/api/                 # HTTP + WebSocket 进程
│   ├── cmd/worker/              # Outbox 与异步任务进程
│   ├── internal/domain/         # 领域实体、枚举和仓储端口
│   ├── internal/application/    # 用例接口与 TODO 服务
│   ├── internal/platform/       # MySQL、Redis、MQ、日志
│   ├── internal/realtime/       # WS 协议和连接中心
│   ├── internal/transport/      # Gin 路由、中间件、Handler
│   └── pkg/                     # 可复用的 ID、JWT、密码、校验工具
├── frontend/
│   └── src/
│       ├── app/                 # 应用装配和全局样式
│       ├── features/            # auth/chat/contact/group/profile
│       ├── pages/               # 路由页面
│       └── shared/              # API、WS、类型和通用工具
├── deploy/mysql/init/           # 全量初始化 SQL
├── docs/adr/                    # 可追溯架构决策
├── scripts/                     # 本地开发与个性化脚本
└── .github/workflows/           # GitHub Actions
```

## 快速开始

环境要求：Go 1.24+、Node.js 22+、Docker Compose。

```powershell
Copy-Item .env.example .env
Copy-Item backend/configs/config.example.yaml backend/configs/config.local.yaml

# 基础设施
docker compose up -d mysql redis rabbitmq

# API
Set-Location backend
go run ./cmd/api -config configs/config.local.yaml

# 前端（新终端）
Set-Location frontend
npm ci
npm run dev
```

- Web：`http://localhost:5173`
- API 健康检查：`http://localhost:18080/healthz`
- RabbitMQ 管理台：`http://localhost:15672`

接口结构也可直接查看 [`docs/openapi.yaml`](docs/openapi.yaml)。

## 开发入口

| 目标 | 命令 |
| --- | --- |
| 后端检查 | `go test ./...`（在 `backend` 下） |
| 前端检查 | `npm run check`（在 `frontend` 下） |
| 全部检查 | `./scripts/check.ps1` |
| 基础设施 | `docker compose up -d mysql redis rabbitmq` |
| 完整容器 | `docker compose --profile app up --build` |

未实现的业务清单集中在 [开发路线](docs/roadmap.md)。建议按里程碑提交，每个 PR 只关闭一组 TODO，这样 GitHub 历史能真实反映你的设计和实现过程。

## 发布前个性化

```powershell
./scripts/personalize.ps1 -GitHubUser "你的 GitHub 用户名" -Author "你的名字"
```

然后检查仓库名、截图、联系方式和部署域名。脚本会替换模块路径与许可证中的占位符，但不会自动创建远端仓库。

## License

[MIT](LICENSE) © 2026 `xiaoliang`
