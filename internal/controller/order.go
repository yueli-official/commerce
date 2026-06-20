// Package controller holds the GoFrame struct-based HTTP handlers for the
// commerce service.
package controller

import (
	"context"

	"platform/gokit/authjwt"
	"platform/gokit/errs"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/gateway"
	"platform/services/commerce/internal/service"
)

// Order handles POST /api/v1/orders.
type Order struct {
	svc       *service.Service
	registry  gateway.Registry
	notifyURL string
	returnURL string
}

// NewOrder constructs an Order controller.
// notifyURL is the full URL that the payment provider will POST the async
// notify to (e.g. "https://example.com/api/v1/payments/alipay/notify").
// returnURL is the URL the buyer is redirected to after paying.
func NewOrder(svc *service.Service, reg gateway.Registry, notifyURL, returnURL string) *Order {
	return &Order{svc: svc, registry: reg, notifyURL: notifyURL, returnURL: returnURL}
}

// CreateOrder handles POST /api/v1/orders (user JWT required).
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
	}

	order, _, err := c.svc.CreateOrder(ctx, sub, desc)
	if err != nil {
		return nil, err
	}

	gw, ok := c.registry["alipay"]
	if !ok {
		return nil, errs.New(commerceerr.CodeGatewayFailed, "alipay gateway not registered", nil)
	}

	payURL, err := gw.CreatePayment(ctx, gateway.CreateIn{
		OrderNo:     order.OrderNo,
		Subject:     req.Title,
		AmountCents: order.AmountCents,
		NotifyURL:   c.notifyURL,
		ReturnURL:   c.returnURL,
	})
	if err != nil {
		return nil, errs.New(commerceerr.CodeGatewayFailed, "payment gateway error", nil)
	}

	if err := c.svc.SetPaying(ctx, order.OrderNo); err != nil {
		return nil, err
	}

	return &v1.CreateOrderRes{
		OrderNo: order.OrderNo,
		PayURL:  payURL,
	}, nil
}
