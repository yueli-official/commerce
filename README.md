# paykit

`paykit` 是供 Yueli 后端服务复用的支付 provider 工具包，负责 provider 中立的支付契约及具体 adapter：

- `paykit.Provider` 与 `paykit.Registry`
- 创建支付会话
- 浏览器按钮或服务端捕获
- provider 通知与 webhook 验证
- 退款
- 测试用 fake provider
- `providers/*` 下的支付宝、PayPal 与微信 adapter

## 边界

拥有支付编排职责的后端服务（例如 `services/commerce`）可以使用 `paykit`。`products/shop/web` 等商店前端和 `products/shop/api` 等目录服务不得直接导入；它们应调用 Commerce 的结算与订单接口。Commerce 负责订单快照、金额验证、履约、支付事件、退款和未来清结算账本。

## 示例

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
```

## 当前 Provider

- `providers/alipay`：支付宝网页支付与异步通知验证。
- `providers/paypal`：PayPal Orders 创建、捕获和退款。
- `providers/wechat`：微信 Native 二维码、异步通知验证和退款。

验证命令：`go test ./packages/go/paykit/...`。
