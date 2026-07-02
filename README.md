# paykit

`paykit` is the reusable payment-provider toolkit for Yueli services.

It owns provider-neutral payment contracts and concrete provider adapters:

- `paykit.Provider`
- `paykit.Registry`
- create payment session
- browser-button/server capture
- provider notify/webhook verification
- refund
- fake provider for tests
- Alipay, PayPal, and WeChat adapters under `providers/*`

## Boundary

Use `paykit` from backend services that own payment orchestration, such as
`services/commerce`.

Do not import `paykit` from storefront apps such as `apps/shop`, or from catalog
services such as `services/shop`. Storefronts should call commerce checkout and
order APIs. Commerce owns order snapshots, amount validation, fulfillment,
payment events, refunds, and future settlement ledgers.

## Example

```go
reg := paykit.NewRegistry()

paypalProvider, err := paypal.NewProvider(paypal.Config{
    ClientID:     cfg.ClientID,
    ClientSecret: cfg.ClientSecret,
    Sandbox:      cfg.Sandbox,
})
if err != nil {
    return err
}
if err := reg.Register(paypalProvider); err != nil {
    return err
}

provider, ok := reg.Get("paypal")
if !ok {
    return fmt.Errorf("paypal provider is not registered")
}

payment, err := provider.CreatePayment(ctx, paykit.CreatePaymentIn{
    OrderNo:     order.OrderNo,
    Subject:     order.Subject,
    AmountCents: order.AmountCents,
    Currency:    order.Currency,
    NotifyURL:   notifyURL,
    ReturnURL:   returnURL,
})
```

## Current Providers

- `providers/alipay`: Alipay page-pay and async notify verification.
- `providers/paypal`: PayPal Orders create/capture/refund.
- `providers/wechat`: WeChat native QR, async notify verification, and refund.
