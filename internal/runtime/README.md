# Commerce 运行时适配

本目录拥有 Commerce 对 Foundation 通用能力的服务级组合，不包含订单、支付、积分或权益规则。

- `auth.go`：组合 Foundation JWKS source、token verifier 与 GoFrame 认证中间件；
- `health.go`：组合 PostgreSQL readiness 与 Foundation health runner；
- `http.go`：组合 Foundation Problem/GoFrame 中间件与进程限流策略；
- `openapi.go`：处理显式 OpenAPI 导出；
- `postgres.go`：从 Commerce 的 GoFrame 配置创建标准 PostgreSQL 连接；
- `telemetry.go`：读取 Commerce 运行环境并组装 Foundation telemetry provider；
- `webhook.go`：组合 Foundation webhook、持久化 work adapter、密钥存储和投递 runner。

这些薄适配随 Commerce 仓迁移，避免服务依赖 Platform `gokit`，也避免把 Commerce 的配置键和进程策略放进
Foundation。验证只运行聚焦 package 测试和编译，不启动数据库或 HTTP 服务。
