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
	for _, item := range req.Items {
		desc.Items = append(desc.Items, service.CheckoutItemDesc{
			SiteKey: item.SiteKey, ExternalID: item.ExternalID, VariantID: item.VariantID,
			Title: item.Title, VariantTitle: item.VariantTitle, SKU: item.SKU,
			PriceCents: item.PriceCents, PointsCost: item.PointsCost, Currency: item.Currency,
			DeliveryKind: item.DeliveryKind, DeliveryRef: item.DeliveryRef, Quantity: item.Quantity,
		})
	}

	order, err := c.svc.CreateCheckout(ctx, desc)
	if err != nil {
		return nil, err
	}

	payment, err := gw.CreatePayment(ctx, gateway.CreateIn{
		OrderNo:     order.OrderNo,
		Subject:     checkoutSubject(req.Items),
		AmountCents: order.AmountCents,
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
