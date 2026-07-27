# 交易服务

- 生命周期：活跃的业务服务
- 权威来源：本仓源码、版本化迁移、`doctor.yaml` 和生成的 OpenAPI
- 消费者：Shop、Resource、交付页面与运营工具
- 验证：`go test ./...`

Commerce 负责订单、支付、积分、权益、交付授权、退款与支付恢复。产品服务拥有商品和内容目录，并向 Commerce
提供可信的当前商品快照；Commerce 不拥有产品页面、商品编辑状态或资源文件。支付 provider 合同和适配器属于
Commerce，必须与订单及履约语义按同一服务版本演进。

## 运行模型

- PostgreSQL 是订单、支付观察、积分账本、权益、交付、恢复任务、Webhook 和审计记录的真值来源。
- Identity 提供 OIDC/JWKS 与机器身份；Commerce 在本地验证 token。
- Asset 提供私有资源交付授权与撤销；Commerce 保存远程 grant 与本地订单/交付授权的绑定。
- Notification 是可选通知 provider。通知失败不会回滚已完成的交易或交付。
- Shop 在结算时提供可信的当前商品快照，但它是业务调用方，不属于 Commerce 启动期的 Doctor 依赖闭包。
- Alipay、WeChat、PayPal 的 provider-native notify、退款和拒付事实由 Commerce 解释，不改写成通用 Webhook。

## 主要接口组

- `/api/v1/checkouts/*`：付费、积分和免费结算，以及支付 capture/sync。
- `/api/v1/access`、`/credits/*`、`/checkin/*`：权益、积分账本与签到。
- `/api/v1/delivery/*`：交付令牌、下载和订单交付查询。
- `/api/v1/payments/*`：支付 provider 回调。
- `/api/v1/admin/orders/*`：订单、退款、交付和恢复操作。
- `/api/v1/admin/capabilities` 与 `/providers`：能力发现和 provider 探测。
- `/healthz`、`/readyz`、`/api.json`、`/swagger`：运维与接口发现。

完整请求/响应结构以服务生成的 OpenAPI 为准；本说明只描述稳定接口组，不复制字段表。

## 可靠执行与恢复

Foundation Work 驱动支付对账、交付恢复和 Webhook 投递。启用 Webhook 时，Commerce 会在同一 PostgreSQL
事务中提交领域变更和待投递事件；master key 必须是由 secret manager 注入的 32-byte base64 或 hex 值。

Asset grant 撤销失败会进入持久恢复队列，直到远程状态收敛或授权自然过期。统一恢复入口聚合 Payment Attempt、
Refund、Dispute 与 Asset grant；Dispute 只展示 provider 事实，不允许用通用 retry 改写裁决状态。

## 目录地图

- `api/v1/`：请求/响应契约和 OpenAPI 元数据。
- `cmd/commerce/`：进程入口、provider 与 worker 组合。
- `cmd/errorcatalog/`：生成和检查版本化公共错误目录。
- `internal/service/`：订单、积分、权益、交付与退款领域行为。
- `internal/paymentcap/`、`internal/paymentreconcile/`：支付能力和对账恢复。
- `internal/commercewebhook/`、`internal/runtime/`：领域事件定义与服务级 Foundation 适配。
- `contracts/errors/`：11 项版本化公共错误合同。
- `manifest/config/`：配置模板，真实配置被 Git 忽略。
- `manifest/sql/migrations/`：唯一 schema 历史权威。

## 开发

仓库自治清单位于 `doctor.yaml`。安装统筹仓库提供的 Doctor CLI 后，在本仓执行：

```text
doctor check
doctor test
doctor up --detach
doctor status --check
doctor logs commerce
doctor down
```

`check` 不启动进程或修改数据库；缺少运行配置、`COMMERCE_DATABASE_URL`、Identity 或 Asset 地址时会明确警告。
`up` 不会隐式执行 migration，也不会自动拉起 Shop。后台状态和日志只写入仓库本地 `.doctor/`。

把 `manifest/config/config.example.yaml` 复制为忽略的 `config.yaml` 后填写数据库、站点上下文和 provider
secret。Doctor 注入的依赖地址会覆盖模板中的本地地址：

- `IDENTITY_BASE_URL` 派生 issuer、JWKS 与 OAuth token endpoint；
- `ASSET_BASE_URL` 覆盖 Asset API 地址；
- `NOTIFICATION_BASE_URL` 覆盖可选 Notification API 地址。

聚焦验证命令：

```powershell
go run ./cmd/errorcatalog --check
go test ./internal/commerceerr ./internal/controller ./internal/runtime
go test -run '^$' ./...
go build ./cmd/commerce
```

真实数据库、支付 provider、Webhook worker 和多仓启动验收应在明确配置对应 secret 后单独执行，不属于默认
单元门禁。
