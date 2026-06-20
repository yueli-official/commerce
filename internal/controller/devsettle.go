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
	// Load the order to get its amount for the MarkPaid call.
	// We use the service's DAO indirectly via a sentinel provider TX ID.
	// MarkPaid guards against the wrong amount, so we load the order first
	// to pass the correct amount (which matches what was recorded at order time).
	o, err := c.svc.GetOrderByNo(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	if err := c.svc.MarkPaid(ctx, req.OrderNo, "DEV-SETTLE-"+req.OrderNo, o.AmountCents); err != nil {
		return nil, err
	}
	return &v1.DevSettleRes{}, nil
}
