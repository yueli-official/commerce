package controller

import (
	"context"

	"platform/gokit/authjwt"
	"platform/paykit"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/model"
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
	orders, err := c.svc.ListOrders(ctx, req.Status, req.Q, req.Limit, req.Offset)
	if err != nil {
		return nil, err
	}
	res := &v1.AdminListOrdersRes{Orders: make([]v1.AdminOrderView, 0, len(orders))}
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
	order, err := c.svc.GetOrderByNo(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	gw, ok := c.registry[order.PaymentProvider]
	if !ok {
		return nil, commerceerr.InvalidRequest("payment provider does not support refund")
	}
	out, err := gw.Refund(ctx, paykit.RefundIn{
		OrderNo:      order.OrderNo,
		ProviderTxID: order.ProviderTxID,
		AmountCents:  order.AmountCents,
		Reason:       req.Reason,
	})
	if err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, commerceerr.InvalidRequest("refund was not accepted")
	}
	if err := c.svc.MarkRefunded(ctx, order.OrderNo, out.ProviderID); err != nil {
		return nil, err
	}
	return &v1.AdminOrderRefundRes{ProviderID: out.ProviderID, Status: model.OrderStatusRefunded}, nil
}

func requireAdmin(ctx context.Context) error {
	p, ok := authjwt.From(ctx)
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
