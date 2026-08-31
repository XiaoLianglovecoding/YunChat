# 工程与基础设施：从零理解 MyIM 的地基

这篇文档对应任务清单第 1 章。目标不是马上实现“注册、聊天”，而是先让后续业务拥有一套可重复、可失败、可观察的运行环境。你可以把业务比作楼上的房间；MySQL、Redis、RabbitMQ、迁移和错误契约就是地基、水电与消防。

## 1. 先看全局：一次启动发生了什么

```text
读取 YAML + 展开环境变量
        │
        v
校验服务名、地址、连接池、超时
        │
        v
连接 MySQL ──> 执行 001..012 迁移 ──失败──> 退出，不监听 HTTP
        │
        v
连接 Redis ──> Ping ──> 加载 6 个 Lua 脚本并核对 SHA
        │
        v
连接 RabbitMQ ──> 声明主队列、DLX、DLQ、开启 Confirm
        │
        v
组装 Repository 端口 ──> 注册路由 ──> 启动 HTTP 与本机 pprof
```

入口在 `backend/cmd/server/main.go`。`main` 只负责“把零件接起来”，业务规则以后放在 Service 中。这个做法叫依赖装配：Service 只认识 `MySQLRepository` 等接口，不关心底下是 MySQL 还是测试替身。

退出时顺序反过来：先停止 HTTP 接收请求，再关 pprof，最后按 RabbitMQ → Redis → MySQL 释放依赖。先关上层可以避免它继续向已经关闭的下层写数据。

## 2. 配置：为什么不能把数字散落在代码里

配置结构在 `backend/internal/config/config.go`，示例在 `backend/configs/config.local.example.yaml`。

以查询超时为例：

```yaml
mysql:
  connect_timeout_ms: 3000
  query_timeout_ms: 3000
  max_open_conns: 50
  max_idle_conns: 10
```

- `connect_timeout_ms`：建立连接最多等多久。
- `query_timeout_ms`：一条 SQL 最多执行多久。
- `max_open_conns`：连接池最多同时打开多少连接。
- `max_idle_conns`：暂时没工作但保留复用的连接数量。

连接池不是“连接越多越快”。连接过多会把压力直接推给 MySQL；连接过少会让请求排队。这里的数值是开发默认值，生产环境要根据压测和数据库容量调整。

`Load` 做三件事：读 YAML、用环境变量替换 `${NAME}`、填默认值并校验。代码不会打印完整配置，避免密码或 Token 进入日志。

## 3. 数据库迁移：给表结构建立升级履历

迁移器在 `backend/internal/migrate/migrate.go`。每个 SQL 文件名由“版本号 + 名称”构成，例如 `011_foundation_schema.sql`。

第一次运行会创建：

```sql
schema_migrations(version, name, checksum, dirty, applied_at)
```

关键字段：

- `version`：迁移版本，保证每版只应用一次。
- `checksum`：SQL 文件的 SHA-256。已经上线的迁移被偷偷修改时，启动会失败。
- `dirty`：开始执行前写 1，全部成功后改 0。MySQL DDL 经常隐式提交，失败时可能只完成一半，因此不能假装回滚成功。

常用命令：

```powershell
Set-Location 'E:\IT\my_IM\backend'
go run ./cmd/migrate -c configs/config.local.yaml status
go run ./cmd/migrate -c configs/config.local.yaml up
go run ./cmd/migrate -c configs/config.local.yaml up  # 再执行一次应安全跳过
```

如果看到 `DIRTY`，不要直接删除记录。先检查失败 SQL 已经改了哪些列或索引，修复数据库到明确状态，再决定人工标记或重做。这样虽然麻烦，却能避免服务带着半套 Schema 接流量。

历史迁移 001..010 被保留，011 做基础升级修正，012 增加好友分页索引和缓存协调事件；`backend/scripts/baseline/000_final_schema.sql` 是最终结构的阅读参考，不由程序执行。这样既能升级旧库，也能让新同学快速看懂最终 15 张业务/协调表。

## 4. MySQL Repository：连接池、超时和事务

连接创建在 `backend/internal/infra/mysql.go`，CRUD 在 `backend/internal/repository/mysql_repository.go`，公共保护层在 `mysql_runner.go`。

### 4.1 Repository 是什么

Repository 把 SQL 藏在数据访问层，对 Service 提供“创建用户”“是否好友”等方法。好处是业务层不用到处拼 SQL，测试时也能替换成假的接口实现。

### 4.2 查询超时如何生效

`timedRunner` 包住 `ExecContext`、`QueryContext` 和 `QueryRowContext`。调用者没有更短截止时间时，它会自动增加 `query_timeout_ms`。慢 SQL 到期后 context 被取消，连接不再无限占用。

### 4.3 唯一键冲突为何要映射

MySQL 的重复键错误号是 1062。底层将它映射为 `repository.ErrConflict`。Service 以后结合业务场景再映射，例如注册用户名冲突变为业务码 1103。不要在 Handler 中解析数据库错误字符串，因为不同版本和语言的文本会变化。

### 4.4 事务解决什么问题

接受好友申请需要“更新申请 + 创建双向好友”同时成功。若第二步失败、第一步却已提交，数据就矛盾了。

```go
repo.WithinTransaction(ctx, func(ctx context.Context, txRepo repository.MySQLRepository) error {
    // 这里对 txRepo 的所有调用绑定同一个 sql.Tx。
    // 返回 error 自动 Rollback；全部成功自动 Commit。
    return nil
})
```

请注意回调参数是 `txRepo`，不是外层的 `repo`。误用外层对象会绕开事务。

## 5. Redis 与 Lua：把多个命令变成一个原子操作

Redis 客户端在 `backend/internal/infra/redis.go`，Repository 在 `redis_repository.go` 和 `redis_extensions.go`。

普通命令可能被别的请求插入。例如“检查是否点赞 → 增加集合 → 点赞数加一”拆开执行时，并发重试会重复计数。Lua 在 Redis 单线程中一次完成这组动作，因此对外表现为原子操作。

项目只保留 `backend/internal/redis/lua_*.go` 这一套脚本来源。启动时会：

1. 为 6 个脚本计算 SHA-1；
2. 调用 `SCRIPT LOAD`；
3. 比对 Redis 返回的 SHA；
4. 任一脚本失败就阻止启动；
5. 在结构化启动日志记录“脚本名 → SHA”。

执行使用 go-redis 的 `Script.Run`：通常走 `EVALSHA`，Redis 重启导致脚本缓存消失时会自动回退加载。好友、黑名单、群成员缓存已经在 `CACHE-001` 中实现启动预热、按需回源、事务协调事件与管理命令；详见 `FRIEND_CACHE_TUTORIAL.md`。

## 6. RabbitMQ：发布成功不等于“函数没报错”

实现位于 `backend/internal/infra/rabbitmq.go`。

一个可靠发布要经过：

```text
JSON 编码
  -> mandatory 发布
  -> 队列不存在？Basic.Return，判定失败
  -> Broker 落盘处理
  -> Publisher Confirm ACK/NACK
  -> 超时或断线时有限重试
```

- `persistent`：消息标为持久化。
- `mandatory`：路由不到队列时必须退回，不能静默丢失。
- `Confirm`：Broker 明确 ACK 后才算成功。
- `publish_timeout_ms`：永远不能无限等待确认。
- `retry_count`：重试有上限，避免故障时形成无限风暴。

当前实际使用 4 条主队列，每条都有 `.dlq` 死信队列，并通过 `my_im.dlx` 路由。原项目的 `comment_persist` 没有生产者和消费者，本实现明确删除；评论任务采用同步 MySQL 写入。

基础版用互斥锁让一个 AMQP channel 同时只有一个等待中的 Confirm，优先保证确认与返回消息不串号。未来压测需要更高吞吐时，可以升级为 channel pool，但不能牺牲确认语义。

## 7. `/health` 与 `/ready` 为什么要分开

- `/health`：进程还活着就返回 200，供“是否需要重启”判断。
- `/ready`：MySQL、Redis、RabbitMQ 全部检查成功才返回 200，供负载均衡决定“是否把用户请求送进来”。

依赖故障时 `/ready` 返回 503，例如：

```json
{
  "status": "not_ready",
  "service": "my-im",
  "dependencies": {"mysql":"ok","redis":"error","rabbitmq":"ok"}
}
```

响应只暴露 `ok/error`，详细错误写日志，避免把地址或内部细节直接交给客户端。

## 8. 错误码：HTTP 状态与业务码是两件事

契约在 `backend/internal/apperror/errors.go`。例如用户名已存在：

```text
HTTP status = 409 Conflict
JSON code   = 1103
```

HTTP 状态让代理、浏览器和通用监控理解结果；业务码让前端准确显示产品提示。不能把 1103 当 HTTP 状态，也不能所有错误都返回 HTTP 200。未知错误对外统一为安全的 1000 文案，原始 cause 只留给服务端日志。

WebSocket 的 4001..5003 也使用同一份类型化目录。前端 `goim-ws-types.ts` 的枚举已经补齐 1308..1310 和 1506。

## 9. 可观测性：出错后要能回答“哪里慢、哪里坏”

每个 HTTP 请求都会获得 `X-Request-ID`，结构化 JSON 日志只记录请求 ID、方法、路由模板、状态、耗时和客户端 IP，不记录 Authorization、密码或消息正文。

访问指标：

```powershell
Invoke-WebRequest 'http://localhost:18080/metrics'
```

核心指标包括 HTTP 请求/错误、数据库次数/错误/总耗时、MQ 发布/重试/错误、WS 当前连接、消费者失败与各队列积压。WS 和 Consumer 业务尚未实现时，对应值为 0，但插桩入口已经准备好。

pprof 默认监听 `127.0.0.1:16060`，与业务端口隔离：

```powershell
go tool pprof http://127.0.0.1:16060/debug/pprof/profile?seconds=10
```

## 10. 建议你亲手做一次的实验

```powershell
Set-Location 'E:\IT\my_IM'
Copy-Item .env.example .env
docker compose up -d mysql redis rabbitmq

Set-Location .\backend
Copy-Item configs\config.local.example.yaml configs\config.local.yaml
go run ./cmd/migrate -c configs/config.local.yaml up
go run ./cmd/migrate -c configs/config.local.yaml status
go run ./cmd/server -c configs/config.local.yaml
```

另开一个终端：

```powershell
Invoke-RestMethod http://localhost:18080/health
Invoke-RestMethod http://localhost:18080/ready
Invoke-WebRequest http://localhost:18080/metrics
```

然后停止 Redis：`docker compose stop redis`。再次请求 `/health` 应仍为 200，而 `/ready` 应变成 503。启动 Redis 后会恢复；这就是存活与就绪的区别。

## 11. 常见问题

- `connection refused`：先看 `docker compose ps`，再核对本机端口是 MySQL 13306、Redis 16379、AMQP 5673。
- `migration ... is dirty`：不要删记录蒙混过去，按第 3 节检查部分 DDL。
- RabbitMQ `PRECONDITION_FAILED`：已有同名队列的参数与新 DLQ 参数不同。学习环境可在确认无数据后重建对应队列；有数据环境必须先制定迁移方案。
- `/ready` 是 503：看 JSON 中是哪一项 error，再用结构化日志查详细 cause。
- 前端仍返回 501：这是正常的。第一章只完成地基，注册/好友/消息 Service 仍保留对应 `TODO[ID]`。
