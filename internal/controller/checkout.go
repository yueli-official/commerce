package controller

import (
	"context"
	"strings"

	"github.com/gogf/gf/v2/frame/g"

	"platform/gokit/authjwt"
	"platform/gokit/errs"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/gateway"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/service"
)

type Checkout struct {
	svc       *service.Service
	registry  gateway.Registry
	notifyURL string
}

func NewCheckout(svc *service.Service, reg gateway.Registry, notifyURL string) *Checkout {
	return &Checkout{svc: svc, registry: reg, notifyURL: notifyURL}
}

func (c *Checkout) CreateCheckout(ctx context.Context, req *v1.CreateCheckoutReq) (*v1.CreateCheckoutRes, error) {
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = "alipay"
	}
	if provider == model.ProductKindPoints {
		return nil, commerceerr.InvalidRequest("use /api/v1/checkouts/points for points checkout")
	}
	gw, ok := c.registry[provider]
	if !ok {
		return nil, commerceerr.InvalidRequest("unsupported payment provider")
	}

	desc := service.CheckoutDesc{
		BuyerEmail: strings.TrimSpace(req.BuyerEmail),
		Provider:   provider,
		ReturnURL:  req.ReturnURL,
		CancelURL:  req.CancelURL,
		Items:      make([]service.CheckoutItemDesc, 0, len(req.Items)),
	}
	if p, ok := authjwt.From(ctx); ok && p != nil {
		desc.BuyerSub = p.Subject
	}
	desc.Items = checkoutItems(req.Items)

	order, err := c.svc.CreateCheckout(ctx, desc)
	if err != nil {
		return nil, err
	}

	payment, err := gw.CreatePayment(ctx, gateway.CreateIn{
		OrderNo:     order.OrderNo,
		Subject:     checkoutSubject(req.Items),
		AmountCents: order.AmountCents,
		Currency:    order.Currency,
		NotifyURL:   notifyURLFor(provider, c.notifyURL),
		ReturnURL:   req.ReturnURL,
	})
	if err != nil {
		g.Log().Errorf(ctx, "checkout CreatePayment failed for order %s provider %s: %+v", order.OrderNo, provider, err)
		cancelBestEffort(ctx, c.svc, order.OrderNo)
		return nil, errs.New(commerceerr.CodeGatewayFailed, "payment gateway error", nil)
	}
	if payment.SessionID != "" {
		if err := c.svc.SetCheckoutPaymentSession(ctx, order.OrderNo, payment.SessionID); err != nil {
			cancelBestEffort(ctx, c.svc, order.OrderNo)
			return nil, err
		}
	}

	return &v1.CreateCheckoutRes{
		OrderNo: order.OrderNo, AmountCents: order.AmountCents, Currency: order.Currency,
		Provider: payment.Provider, Method: payment.Method, PayURL: payment.PayURL,
		SessionID: payment.SessionID, QRCode: payment.QRCode, ClientToken: payment.ClientToken,
	}, nil
}

func (c *Checkout) CreatePointsCheckout(ctx context.Context, req *v1.CreatePointsCheckoutReq) (*v1.CreatePointsCheckoutRes, error) {
	p, ok := authjwt.From(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	res, err := c.svc.RedeemCheckout(ctx, service.CheckoutDesc{
		BuyerSub: p.Subject, BuyerEmail: strings.TrimSpace(req.BuyerEmail), Provider: model.ProductKindPoints,
		Items: checkoutItems(req.Items),
	})
	if err != nil {
		return nil, err
	}
	return &v1.CreatePointsCheckoutRes{
		OrderNo: res.Order.OrderNo, Token: res.Grant.Token, DeliveryRef: res.Grant.DeliveryRef,
		State: res.Grant.State, Balance: int(res.Balance),
	}, nil
}

func (c *Checkout) CaptureCheckout(ctx context.Context, req *v1.CaptureCheckoutReq) (*v1.CaptureCheckoutRes, error) {
	order, err := c.svc.GetOrderByNo(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	if order.Status != model.OrderStatusPaying {
		return nil, commerceerr.OrderInvalidState(order.Status, model.OrderStatusFulfilled)
	}
	if order.PaymentProvider != "" && order.PaymentProvider != req.Provider {
		return nil, commerceerr.InvalidRequest("payment provider mismatch")
	}
	if order.PaymentSessionID != "" && order.PaymentSessionID != req.SessionID {
		return nil, commerceerr.InvalidRequest("payment session mismatch")
	}
	gw, ok := c.registry[req.Provider]
	if !ok {
		return nil, commerceerr.InvalidRequest("unsupported payment provider")
	}
	capture, err := gw.CapturePayment(ctx, gateway.CapturePaymentIn{
		OrderNo: req.OrderNo, SessionID: req.SessionID, AmountCents: order.AmountCents,
	})
	if err != nil {
		return nil, errs.New(commerceerr.CodeGatewayFailed, "payment gateway error", nil)
	}
	if !capture.Success {
		return nil, commerceerr.NotifyInvalid("payment capture failed")
	}
	amount := capture.AmountCents
	if amount == 0 {
		amount = order.AmountCents
	}
	grant, err := c.svc.SettleCheckout(ctx, req.OrderNo, req.Provider, capture.ProviderTxID, amount)
	if err != nil {
		return nil, err
	}
	return &v1.CaptureCheckoutRes{
		OrderNo: req.OrderNo, Token: grant.Token, DeliveryRef: grant.DeliveryRef, State: grant.State,
	}, nil
}

func (c *Checkout) DeliveryByToken(ctx context.Context, req *v1.DeliveryByTokenReq) (*v1.DeliveryByTokenRes, error) {
	delivery, err := c.svc.DeliveryByToken(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	return &v1.DeliveryByTokenRes{Delivery: deliveryView(delivery)}, nil
}

func (c *Checkout) DeliveryDownload(ctx context.Context, req *v1.DeliveryDownloadReq) (*v1.DeliveryDownloadRes, error) {
	download, err := c.svc.ResolveDeliveryDownload(ctx, req.Token, req.Exp, req.Sig)
	if err != nil {
		return nil, err
	}
	return &v1.DeliveryDownloadRes{
		DeliveryRef: download.DeliveryRef,
		ExpiresAt:   download.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (c *Checkout) MyPurchases(ctx context.Context, req *v1.MyPurchasesReq) (*v1.MyPurchasesRes, error) {
	p, ok := authjwt.From(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	deliveries, err := c.svc.Purchases(ctx, p.Subject, req.Limit, req.Offset)
	if err != nil {
		return nil, err
	}
	res := &v1.MyPurchasesRes{Purchases: make([]v1.DeliveryView, 0, len(deliveries))}
	for _, delivery := range deliveries {
		res.Purchases = append(res.Purchases, deliveryView(delivery))
	}
	return res, nil
}

func checkoutItems(items []v1.CheckoutItemReq) []service.CheckoutItemDesc {
	out := make([]service.CheckoutItemDesc, 0, len(items))
	for _, item := range items {
		out = append(out, service.CheckoutItemDesc{
			SiteKey: item.SiteKey, ExternalID: item.ExternalID, VariantID: item.VariantID,
			Title: item.Title, VariantTitle: item.VariantTitle, SKU: item.SKU,
			PriceCents: item.PriceCents, PointsCost: item.PointsCost, Currency: item.Currency,
			DeliveryKind: item.DeliveryKind, DeliveryRef: item.DeliveryRef, Quantity: item.Quantity,
		})
	}
	return out
}

func deliveryView(delivery *service.DeliveryResult) v1.DeliveryView {
	view := v1.DeliveryView{
		OrderNo: delivery.Order.OrderNo, BuyerEmail: delivery.Order.BuyerEmail, DeliveryRef: delivery.Grant.DeliveryRef,
		State: delivery.Grant.State, CreatedAt: delivery.Grant.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if delivery.DownloadURL != "" {
		view.DownloadURL = delivery.DownloadURL
	}
	if delivery.DownloadExpiresAt != nil {
		view.DownloadExpiresAt = delivery.DownloadExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if delivery.Item != nil {
		view.Title = delivery.Item.TitleSnapshot
		view.VariantTitle = delivery.Item.VariantTitleSnapshot
		view.SKU = delivery.Item.SKUSnapshot
		view.DeliveryKind = delivery.Item.DeliveryKindSnapshot
	}
	if view.DeliveryKind == "" {
		view.DeliveryKind = "asset_file"
	}
	return view
}

func checkoutSubject(items []v1.CheckoutItemReq) string {
	if len(items) == 0 {
		return "Virtual goods checkout"
	}
	if len(items) == 1 {
		if items[0].VariantTitle != "" {
			return items[0].Title + " - " + items[0].VariantTitle
		}
		return items[0].Title
	}
	return items[0].Title + " and more"
}

func notifyURLFor(provider, base string) string {
	if provider == "alipay" {
		return base
	}
	return ""
}
