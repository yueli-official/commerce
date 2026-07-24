# 交易服务

- 生命周期：活跃的平台服务
- 权威来源：Catalog `platformServices.commerce`、迁移和 OpenAPI
- 消费者：Shop、Resource 和运营工具
- 验证：`go test ./services/commerce/...`

Commerce 负责积分与签到、虚拟结算、支付方式、交付访问规则和站点实例交易上下文。产品拥有自己的目录对象，并把稳定引用交给 Commerce；Commerce 不拥有产品页面或文件。

`api/v1/` 定义契约，`internal/service/` 负责业务行为，`internal/paymentcap/` 负责 provider 能力发现，`manifest/sql/migrations/` 是 schema 权威。正常启动由 Catalog 驱动；隔离开发使用 `manifest/config/config.example.yaml`。

可选的 Foundation Webhook Runtime 将 `order.fulfilled` 与 `order.refunded` 薄事件和订单/交付领域变更写入
同一 PostgreSQL 事务；Alipay、WeChat、PayPal 的原生 notify/refund 真值仍由 Commerce 解释。启用前依次
执行 `0007_work_v1`、`0008_webhook_v1`，设置 `commerce.webhook.enabled=true`，并从部署 secret manager
注入 32-byte base64/hex `commerce.webhook.masterKey`。endpoint、subscription 与权限化管理入口由部署方
显式接入，不通过 Catalog 共享运行数据。
