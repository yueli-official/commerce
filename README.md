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

Asset 下载授权由 Commerce 保存 `grantId` 与本地订单/交付授权的关系。全额退款、拒付暂停/败诉和管理员
撤销会在同一事务把未过期远程授权写成 `revoke_pending`；Foundation Work 每分钟重试 Asset 撤销直到
`revoked` 或自然过期。第三次失败起可通过 `commerce.recovery.notification.recipientEmail` 通知运营人员。
管理员可用 `GET /api/v1/admin/commerce/recovery/asset-grants` 查询恢复状态，并用对应的 `/{id}/retry`
受保护入口提前重试。撤销排队、远程收敛和人工重试写入 Foundation Audit，审计 schema 由
`0014_audit_v1` 管理。

统一运营入口 `GET /api/v1/admin/commerce/recovery/cases` 聚合待处理 Payment Attempt、Refund、
Dispute 与 Asset grant，并暴露失败次数、最近错误和下次动作时间。Payment、Refund、Asset grant
可通过 `POST /api/v1/admin/commerce/recovery/cases/{kind}/{id}/retry` 提前恢复；Dispute 只展示
provider 事实，不允许用通用 retry 人为改写裁决状态。
