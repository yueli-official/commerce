# Commerce service

- Lifecycle: active platform service
- Authority: Catalog `platformServices.commerce`, migrations and OpenAPI
- Consumers: Shop, Resource and operator tooling
- Verify: `go test ./services/commerce/...`

Commerce owns credits/check-in, virtual checkout, payment methods, delivery
access rules and site-instance commerce context. Products own their catalog
objects and pass stable references into Commerce; Commerce does not own product
pages or files.

`api/v1/` defines contracts, `internal/service/` owns business behavior,
`internal/paymentcap/` owns provider capability discovery, and
`manifest/sql/migrations/` is the schema authority. Normal startup is through
Catalog; isolated work uses `manifest/config/config.example.yaml`.
