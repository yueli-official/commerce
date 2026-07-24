package controller

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	foundationauth "github.com/yueli-official/foundation/go/auth"
	"platform/gokit/errs"
	"platform/paykit"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/paymentrecovery"
	"platform/services/commerce/internal/service"
)

type AdminOrder struct {
	svc      *service.Service
	registry paykit.Registry
}

func NewAdminOrder(svc *service.Service, reg paykit.Registry) *AdminOrder {
	return &AdminOrder{svc: svc, registry: reg}
}

func (c *AdminOrder) ListOrders(ctx context.Context, req *v1.AdminListOrdersReq) (*v1.AdminListOrdersRes, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	orders, total, err := c.svc.ListOrders(ctx, req.Status, req.Q, req.Limit, req.Offset)
	if err != nil {
		return nil, err
	}
	res := &v1.AdminListOrdersRes{Orders: make([]v1.AdminOrderView, 0, len(orders)), Total: total}
	for _, order := range orders {
		items, err := c.svc.OrderItems(ctx, order.ID)
		if err != nil {
			return nil, err
		}
		res.Orders = append(res.Orders, adminOrderView(order, items, nil, nil))
	}
	return res, nil
}

func (c *AdminOrder) OrderDetail(ctx context.Context, req *v1.AdminOrderDetailReq) (*v1.AdminOrderDetailRes, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	detail, err := c.svc.OrderDetail(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	return &v1.AdminOrderDetailRes{Order: adminOrderView(detail.Order, detail.Items, detail.Events, detail.Grants)}, nil
}

func (c *AdminOrder) ResendDelivery(ctx context.Context, req *v1.AdminOrderDeliveryResendReq) (*v1.AdminOrderDeliveryResendRes, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	grant, err := c.svc.ResendDelivery(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	return &v1.AdminOrderDeliveryResendRes{Token: grant.Token, DeliveryRef: grant.DeliveryRef}, nil
}

func (c *AdminOrder) RevokeDelivery(ctx context.Context, req *v1.AdminOrderDeliveryRevokeReq) (*v1.AdminOrderDeliveryRevokeRes, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	n, err := c.svc.RevokeDelivery(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	return &v1.AdminOrderDeliveryRevokeRes{Revoked: int(n)}, nil
}

func (c *AdminOrder) GrantDelivery(ctx context.Context, req *v1.AdminOrderDeliveryGrantReq) (*v1.AdminOrderDeliveryGrantRes, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	grant, err := c.svc.GrantDelivery(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	return &v1.AdminOrderDeliveryGrantRes{Token: grant.Token, DeliveryRef: grant.DeliveryRef}, nil
}

func (c *AdminOrder) RefundOrder(ctx context.Context, req *v1.AdminOrderRefundReq) (*v1.AdminOrderRefundRes, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	principal, _ := foundationauth.FromContext(ctx)
	reserved, err := c.svc.RequestRefund(ctx, service.RefundRequestInput{
		OrderNo: req.OrderNo, AmountCents: req.AmountCents,
		Reason: req.Reason, RequestedBy: principal.Subject,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	order := reserved.Order
	refund := reserved.Refund
	if refund.Status == paymentrecovery.RefundSucceeded {
		return &v1.AdminOrderRefundRes{
			RefundNo: refund.RefundNo, ProviderID: refund.ProviderRefundID,
			Status: string(refund.Status), OrderStatus: order.Status,
		}, nil
	}
	gw, ok := c.registry[order.PaymentProvider]
	if !ok {
		return nil, commerceerr.InvalidRequest("payment provider does not support refund")
	}
	if err := c.svc.MarkRefundSubmitting(ctx, refund.RefundNo); err != nil {
		return nil, err
	}
	out, err := gw.Refund(ctx, paykit.RefundIn{
		OrderNo: order.OrderNo, RefundNo: refund.RefundNo,
		ProviderTxID: order.ProviderTxID, AmountCents: refund.AmountCents,
		TotalAmountCents: order.AmountCents, Currency: order.Currency,
		Reason: refund.Reason, IdempotencyKey: refund.IdempotencyKey,
	})
	if err != nil {
		return nil, errs.New(commerceerr.CodeGatewayFailed, "payment gateway error", nil)
	}
	status, ok := recoveryRefundStatus(out)
	if !ok {
		return nil, commerceerr.InvalidRequest("refund provider returned an unsupported status")
	}
	evidence, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	providerStatus := strings.TrimSpace(out.ProviderStatus)
	if providerStatus == "" {
		providerStatus = string(out.Status)
	}
	accepted, err := c.svc.AcceptRefundObservation(ctx, paymentrecovery.RefundObservation{
		Status: status, Provider: order.PaymentProvider, Merchant: "primary",
		OrderNo: order.OrderNo, RefundNo: refund.RefundNo,
		ProviderRefundID: out.ProviderID,
		Money: paymentrecovery.Money{
			AmountCents: refund.AmountCents, Currency: refund.Currency,
		},
		Source: paymentrecovery.SourceMutation, Authoritative: true,
		IdempotencyKey: refund.RefundNo + ":mutation:" + providerStatus,
		PayloadDigest:  paymentrecovery.DigestPayload(evidence),
		OccurredAt:     time.Now().UTC(),
	}, providerStatus)
	if err != nil {
		return nil, err
	}
	updatedOrder, err := c.svc.GetOrderByNo(ctx, order.OrderNo)
	if err != nil {
		return nil, err
	}
	return &v1.AdminOrderRefundRes{
		RefundNo: accepted.RefundNo, ProviderID: accepted.ProviderRefundID,
		Status: string(accepted.Status), OrderStatus: updatedOrder.Status,
	}, nil
}

func recoveryRefundStatus(out *paykit.RefundOut) (paymentrecovery.RefundStatus, bool) {
	if out == nil {
		return "", false
	}
	switch out.Status {
	case paykit.RefundStatusPending:
		return paymentrecovery.RefundPending, true
	case paykit.RefundStatusSucceeded:
		return paymentrecovery.RefundSucceeded, true
	case paykit.RefundStatusFailed:
		return paymentrecovery.RefundFailed, true
	case paykit.RefundStatusCancelled:
		return paymentrecovery.RefundCancelled, true
	case "":
		if out.Success {
			return paymentrecovery.RefundSucceeded, true
		}
	}
	return "", false
}

func requireAdmin(ctx context.Context) error {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil || !p.HasRole("admin") {
		return commerceerr.Forbidden()
	}
	return nil
}

func adminOrderView(order *model.Order, items []*model.OrderItem, events []*model.PaymentEvent, grants []*model.DeliveryGrant) v1.AdminOrderView {
	view := v1.AdminOrderView{
		ID: order.ID, OrderNo: order.OrderNo, Sub: order.Sub, BuyerSub: order.BuyerSub, BuyerEmail: order.BuyerEmail,
		AmountCents: order.AmountCents, Currency: order.Currency, Status: order.Status,
		PaymentProvider: order.PaymentProvider, DeliveryState: order.DeliveryState,
		CreatedAt: order.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	for _, item := range items {
		view.Items = append(view.Items, v1.AdminOrderItemView{
			ID: item.ID, Title: item.TitleSnapshot, VariantTitle: item.VariantTitleSnapshot,
			SKU: item.SKUSnapshot, Quantity: item.Quantity, PriceCents: item.UnitPriceCents,
			Currency: item.Currency, DeliveryKind: item.DeliveryKindSnapshot, DeliveryRef: item.DeliveryRefSnapshot,
		})
	}
	for _, event := range events {
		view.Events = append(view.Events, v1.AdminPaymentEventView{
			ID: event.ID, Provider: event.Provider, EventType: event.EventType, ProviderEventID: event.ProviderEventID,
			AmountCents: event.AmountCents, Success: event.Success, Message: event.Message,
			CreatedAt: event.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	for _, grant := range grants {
		g := v1.AdminDeliveryGrantView{
			ID: grant.ID, OrderItemID: grant.OrderItemID, BuyerSub: grant.BuyerSub, BuyerEmail: grant.BuyerEmail,
			DeliveryRef: grant.DeliveryRef, State: grant.State, CreatedAt: grant.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if grant.ExpiresAt != nil {
			g.ExpiresAt = grant.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
		}
		if grant.RevokedAt != nil {
			g.RevokedAt = grant.RevokedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		view.Grants = append(view.Grants, g)
	}
	return view
}
