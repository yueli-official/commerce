// Package controller holds the GoFrame struct-based HTTP handlers for the
// commerce service.
package controller

import (
	"context"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"platform/gokit/authjwt"
	"platform/gokit/errs"
	"platform/paykit"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/service"
)

// Order handles POST /api/v1/orders.
type Order struct {
	svc       *service.Service
	registry  paykit.Registry
	notifyURL string
	returnURL string
}

// NewOrder constructs an Order controller.
// notifyURL is the full URL that the payment provider will POST the async
// notify to (e.g. "https://example.com/api/v1/payments/alipay/notify").
// returnURL is the URL the buyer is redirected to after paying.
func NewOrder(svc *service.Service, reg paykit.Registry, notifyURL, returnURL string) *Order {
	return &Order{svc: svc, registry: reg, notifyURL: notifyURL, returnURL: returnURL}
}

// CreateOrder handles POST /api/v1/orders (user JWT required). A `paid` order
// goes through the payment gateway and returns a payUrl; a `points` order is
// redeemed synchronously (spend points → grant entitlement) and returns entitled.
func (c *Order) CreateOrder(ctx context.Context, req *v1.CreateOrderReq) (*v1.CreateOrderRes, error) {
	p, ok := authjwt.From(ctx)
	if !ok || p == nil {
		return nil, errs.New(commerceerr.CodeForbidden, "missing principal", nil)
	}
	sub := p.Subject

	desc := service.OrderDesc{
		SiteKey:    req.SiteKey,
		ExternalID: req.ExternalID,
		Kind:       req.Kind,
		Title:      req.Title,
		Currency:   req.Currency,
		PriceCents: req.PriceCents,
		PointsCost: req.PointsCost,
	}

	// Points redemption: no gateway, synchronous spend → grant.
	if req.Kind == model.ProductKindPoints {
		if req.PointsCost < 1 {
			return nil, commerceerr.InvalidRequest("pointsCost is required for a points order")
		}
		r, err := c.svc.Redeem(ctx, sub, desc)
		if err != nil {
			return nil, err
		}
		bal := int(r.Balance)
		return &v1.CreateOrderRes{Entitled: r.Entitled, Balance: &bal}, nil
	}

	// Paid order: validate price + currency, then go through the gateway.
	if req.PriceCents < 1 {
		return nil, commerceerr.InvalidRequest("priceCents is required for a paid order")
	}
	if req.Currency != "CNY" {
		return nil, commerceerr.InvalidRequest("currency must be CNY")
	}

	order, product, err := c.svc.CreateOrder(ctx, sub, desc)
	if err != nil {
		return nil, err
	}

	gw, ok := c.registry["alipay"]
	if !ok {
		g.Log().Errorf(ctx, "alipay gateway not registered for order %s", order.OrderNo)
		cancelBestEffort(ctx, c.svc, order.OrderNo)
		return nil, errs.New(commerceerr.CodeGatewayFailed, "alipay gateway not registered", nil)
	}

	payment, err := gw.CreatePayment(ctx, paykit.CreatePaymentIn{
		OrderNo:     order.OrderNo,
		Subject:     legacyOrderSubject(product, order),
		AmountCents: order.AmountCents,
		Currency:    order.Currency,
		NotifyURL:   c.notifyURL,
		ReturnURL:   c.returnURL,
	})
	if err != nil {
		g.Log().Errorf(ctx, "alipay CreatePayment failed for order %s: %+v", order.OrderNo, err)
		cancelBestEffort(ctx, c.svc, order.OrderNo)
		return nil, errs.New(commerceerr.CodeGatewayFailed, "payment gateway error", nil)
	}

	if err := c.svc.SetPaying(ctx, order.OrderNo); err != nil {
		g.Log().Errorf(ctx, "SetPaying failed for order %s: %+v", order.OrderNo, err)
		cancelBestEffort(ctx, c.svc, order.OrderNo)
		return nil, err
	}

	return &v1.CreateOrderRes{
		OrderNo: order.OrderNo,
		PayURL:  payment.PayURL,
	}, nil
}

func legacyOrderSubject(product *model.Product, order *model.Order) string {
	if product != nil && strings.TrimSpace(product.Title) != "" {
		return strings.TrimSpace(product.Title)
	}
	if order != nil {
		return "订单 " + order.OrderNo
	}
	return "Virtual goods order"
}

// cancelBestEffort cancels the order on a best-effort basis (gateway failure path).
// If cancellation fails it logs the error but does NOT propagate it — the caller
// already has a gateway error to return to the client.
func cancelBestEffort(ctx context.Context, svc *service.Service, orderNo string) {
	if err := svc.CancelOrder(ctx, orderNo); err != nil {
		g.Log().Errorf(ctx, "failed to cancel orphan order %s after gateway failure: %+v", orderNo, err)
	}
}
