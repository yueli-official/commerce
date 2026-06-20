// Package service implements the commerce domain logic:
// product lazy-create, order state machine, entitlement grant.
package service

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/os/gtime"

	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/model"
)

// OrderDesc carries the caller-supplied descriptor for a new order.
// SiteKey + ExternalID identify the product; PriceCents is the buyer-confirmed price.
type OrderDesc struct {
	SiteKey    string
	ExternalID string
	Kind       string
	Title      string
	Currency   string
	PriceCents int
}

// EntitledResult is the answer to an Entitled query.
type EntitledResult struct {
	Entitled bool
	Reason   string // "ok" | "not_purchased" | "expired"
	Required struct {
		Kind       string
		PriceCents *int // nil when product has not been created yet
	}
}

// Service holds the DAO and implements the commerce domain logic.
type Service struct {
	db *dao.PG
}

// New constructs a Service.
func New(db *dao.PG) *Service {
	return &Service{db: db}
}

// CreateOrder lazily upserts the product from desc, then inserts a new order in
// "pending" status. Returns both the created order and the resolved product.
func (s *Service) CreateOrder(ctx context.Context, sub string, desc OrderDesc) (*model.Order, *model.Product, error) {
	// Lazy upsert product.
	p := &model.Product{
		SiteKey:    desc.SiteKey,
		ExternalID: desc.ExternalID,
		Kind:       desc.Kind,
		Title:      desc.Title,
		PriceCents: desc.PriceCents,
		Currency:   desc.Currency,
		Status:     model.ProductStatusActive,
	}
	if err := s.db.UpsertProduct(ctx, p); err != nil {
		return nil, nil, err
	}

	// Build and insert the order.
	o := &model.Order{
		OrderNo:     NewOrderNo(),
		Sub:         sub,
		ProductID:   p.ID,
		AmountCents: desc.PriceCents,
		Currency:    desc.Currency,
		Status:      model.OrderStatusPending,
	}
	if err := s.db.InsertOrder(ctx, o); err != nil {
		return nil, nil, err
	}
	return o, p, nil
}

// SetPaying transitions an order from "pending" to "paying".
// Called after the payment gateway creates a payment session.
func (s *Service) SetPaying(ctx context.Context, orderNo string) error {
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	if o == nil {
		return commerceerr.OrderNotFound(orderNo)
	}
	if o.Status != model.OrderStatusPending {
		return commerceerr.OrderInvalidState(o.Status, model.OrderStatusPaying)
	}
	return s.db.UpdateOrderStatus(ctx, o.ID, model.OrderStatusPaying)
}

// MarkPaid transitions an order from "paying" to "paid" and grants the entitlement.
// The operation is idempotent: if the order is already "paid", it returns nil without
// re-granting. Amount mismatch returns commerce.notify_invalid.
// Illegal source state (not "paying" and not already "paid") returns commerce.order_invalid_state.
// The status update and entitlement insert are committed in a single transaction.
func (s *Service) MarkPaid(ctx context.Context, orderNo, providerTxID string, amountCents int) error {
	// Load order outside the transaction — if it doesn't exist we bail early.
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	if o == nil {
		return commerceerr.OrderNotFound(orderNo)
	}

	// Idempotent: already paid → success, no re-grant.
	if o.Status == model.OrderStatusPaid {
		return nil
	}

	// Only "paying" orders may be marked paid.
	if o.Status != model.OrderStatusPaying {
		return commerceerr.OrderInvalidState(o.Status, model.OrderStatusPaid)
	}

	// Amount guard.
	if amountCents != o.AmountCents {
		return commerceerr.NotifyInvalid("amount mismatch")
	}

	// Transact: mark paid + grant entitlement.
	// The UPDATE is conditional on status='paying' so that a concurrent delivery
	// that already won the race leaves 0 rows affected → we return nil (safe).
	now := gtime.New(time.Now())
	return s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		updated, err := s.db.ConditionalUpdateOrderStatusTx(
			ctx, tx, o.ID,
			model.OrderStatusPaying, model.OrderStatusPaid,
			providerTxID, now,
		)
		if err != nil {
			return err
		}
		if !updated {
			// Another concurrent delivery already transitioned this order; safe to no-op.
			return nil
		}
		ent := &model.Entitlement{
			Sub:       o.Sub,
			ProductID: o.ProductID,
			Source:    "order",
			OrderID:   &o.ID,
		}
		return s.db.InsertEntitlementTx(ctx, tx, ent)
	})
}

// GetOrderByNo returns the order with the given order number, or an error if not found.
// Used by the dev-settle endpoint to retrieve the order's recorded amount.
func (s *Service) GetOrderByNo(ctx context.Context, orderNo string) (*model.Order, error) {
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, commerceerr.OrderNotFound(orderNo)
	}
	return o, nil
}

// Entitled reports whether sub is entitled to the product identified by (siteKey, externalID).
// Reasons: "ok" (entitled), "not_purchased" (no entitlement or product absent), "expired" (future M2).
func (s *Service) Entitled(ctx context.Context, sub, siteKey, externalID string) (EntitledResult, error) {
	var result EntitledResult

	p, err := s.db.GetProductByExternal(ctx, siteKey, externalID)
	if err != nil {
		return result, err
	}

	// Product not yet created (no one has ever ordered it).
	if p == nil {
		result.Entitled = false
		result.Reason = "not_purchased"
		result.Required.Kind = ""
		result.Required.PriceCents = nil
		return result, nil
	}

	// Fill in the product snapshot for the caller.
	result.Required.Kind = p.Kind
	pc := p.PriceCents
	result.Required.PriceCents = &pc

	exists, err := s.db.EntitlementExists(ctx, sub, p.ID)
	if err != nil {
		return result, err
	}
	if exists {
		result.Entitled = true
		result.Reason = "ok"
	} else {
		result.Entitled = false
		result.Reason = "not_purchased"
	}
	return result, nil
}
