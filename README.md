# 交易服务

- 生命周期：活跃的平台服务
- 权威来源：Catalog `platformServices.commerce`、迁移和 OpenAPI
- 消费者：Shop、Resource 和运营工具
- 验证：`go test ./services/commerce/...`

Commerce 负责积分与签到、虚拟结算、支付方式、交付访问规则和站点实例交易上下文。产品拥有自己的目录对象，并把稳定引用交给 Commerce；Commerce 不拥有产品页面或文件。

`api/v1/` 定义契约，`internal/service/` 负责业务行为，`internal/paymentcap/` 负责 provider 能力发现，`manifest/sql/migrations/` 是 schema 权威。正常启动由 Catalog 驱动；隔离开发使用 `manifest/config/config.example.yaml`。
