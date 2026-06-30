package controller

import (
	"context"

	"platform/gokit/authjwt"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/service"
)

type AdminOrder struct {
	svc *service.Service
}

func NewAdminOrder(svc *service.Service) *AdminOrder {
	return &AdminOrder{svc: svc}
}

func (c *AdminOrder) ListOrders(ctx context.Context, req *v1.AdminListOrdersReq) (*v1.AdminListOrdersRes, error) {
	p, ok := authjwt.From(ctx)
	if !ok || p == nil || !p.HasRole("admin") {
		return nil, commerceerr.Forbidden()
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
		res.Orders = append(res.Orders, adminOrderView(order, items))
	}
	return res, nil
}

func adminOrderView(order *model.Order, items []*model.OrderItem) v1.AdminOrderView {
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
	return view
}
