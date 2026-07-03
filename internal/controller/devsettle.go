package controller

import (
	"context"

	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/service"
)

// DevSettle handles POST /dev/orders/{orderNo}/settle.
// Only registered when commerce.devSettle=true.
// Simulates a successful payment notify by calling MarkPaid directly —
// same internal path as a real notify, but without the network round-trip.
type DevSettle struct {
	svc *service.Service
}

// NewDevSettle constructs a DevSettle controller.
func NewDevSettle(svc *service.Service) *DevSettle {
	return &DevSettle{svc: svc}
}

// Settle handles POST /dev/orders/{orderNo}/settle.
func (c *DevSettle) Settle(ctx context.Context, req *v1.DevSettleReq) (*v1.DevSettleRes, error) {
	// Load the order to pass the recorded amount into the same checkout
	// settlement path used by provider notifications.
	o, err := c.svc.GetOrderByNo(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	grant, err := c.svc.SettleCheckout(ctx, req.OrderNo, o.PaymentProvider, "DEV-SETTLE-"+req.OrderNo, o.AmountCents)
	if err != nil {
		return nil, err
	}
	return &v1.DevSettleRes{Token: grant.Token, DeliveryRef: grant.DeliveryRef}, nil
}
