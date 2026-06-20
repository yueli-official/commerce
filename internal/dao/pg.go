// Package dao is the PostgreSQL data-access layer for the commerce service.
package dao

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/google/uuid"

	"platform/services/commerce/internal/model"
)


// PG wraps the GoFrame gdb handle.
type PG struct{ db gdb.DB }

// NewPG constructs a PG repo from a GoFrame DB handle.
func NewPG(db gdb.DB) *PG { return &PG{db: db} }

// UpsertProduct inserts or updates a product by (site_key, external_id).
// On conflict the title, price_cents, currency, kind, status, and updated_at are refreshed.
// The product ID is written back into p.ID.
func (r *PG) UpsertProduct(ctx context.Context, p *model.Product) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	sql := `
INSERT INTO products (id, site_key, external_id, kind, title, price_cents, currency, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (site_key, external_id) DO UPDATE
  SET kind        = EXCLUDED.kind,
      title       = EXCLUDED.title,
      price_cents = EXCLUDED.price_cents,
      currency    = EXCLUDED.currency,
      status      = EXCLUDED.status,
      updated_at  = now()
RETURNING id`
	val, err := r.db.GetValue(ctx, sql,
		p.ID, p.SiteKey, p.ExternalID, p.Kind, p.Title, p.PriceCents, p.Currency, p.Status,
	)
	if err != nil {
		return err
	}
	p.ID = val.String()
	return nil
}

// GetProductByExternal returns the product matching (siteKey, externalID), or (nil, nil) if absent.
func (r *PG) GetProductByExternal(ctx context.Context, siteKey, externalID string) (*model.Product, error) {
	var p *model.Product
	if err := r.db.Model("products").Ctx(ctx).
		Where("site_key", siteKey).Where("external_id", externalID).
		Limit(1).Scan(&p); err != nil {
		return nil, err
	}
	return p, nil
}

// InsertOrder creates a new order row. p.ID is set if empty.
func (r *PG) InsertOrder(ctx context.Context, o *model.Order) error {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	data := g.Map{
		"id":          o.ID,
		"order_no":    o.OrderNo,
		"sub":         o.Sub,
		"product_id":  o.ProductID,
		"amount_cents": o.AmountCents,
		"currency":    o.Currency,
		"status":      o.Status,
		"gateway":     o.Gateway,
	}
	if o.ProviderTxID != "" {
		data["provider_tx_id"] = o.ProviderTxID
	}
	if o.PaidAt != nil {
		data["paid_at"] = o.PaidAt
	}
	if _, err := r.db.Model("orders").Ctx(ctx).Data(data).Insert(); err != nil {
		return err
	}
	return nil
}

// GetOrderByNo returns the order with the given order_no, or (nil, nil) if absent.
func (r *PG) GetOrderByNo(ctx context.Context, orderNo string) (*model.Order, error) {
	var o *model.Order
	if err := r.db.Model("orders").Ctx(ctx).Where("order_no", orderNo).Limit(1).Scan(&o); err != nil {
		return nil, err
	}
	return o, nil
}

// UpdateOrderStatus sets the status (and optionally paid_at) on an order.
func (r *PG) UpdateOrderStatus(ctx context.Context, orderID, status string) error {
	_, err := r.db.Model("orders").Ctx(ctx).
		Where("id", orderID).
		Data(g.Map{"status": status, "updated_at": gtime.Now()}).
		Update()
	return err
}

// InsertEntitlement inserts an entitlement row. Duplicate (sub, product_id) is silently ignored
// (ON CONFLICT DO NOTHING). e.ID is set if empty.
func (r *PG) InsertEntitlement(ctx context.Context, e *model.Entitlement) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	// Use raw SQL so ON CONFLICT DO NOTHING is expressed directly.
	sql := `
INSERT INTO entitlements (id, sub, product_id, source, order_id, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (sub, product_id) DO NOTHING`
	var orderID interface{}
	if e.OrderID != nil {
		orderID = *e.OrderID
	}
	var expiresAt interface{}
	if e.ExpiresAt != nil {
		expiresAt = e.ExpiresAt
	}
	_, err := r.db.Exec(ctx, sql, e.ID, e.Sub, e.ProductID, e.Source, orderID, expiresAt)
	return err
}

// EntitlementExists reports whether (sub, productID) has an entitlement row.
func (r *PG) EntitlementExists(ctx context.Context, sub, productID string) (bool, error) {
	n, err := r.db.Model("entitlements").Ctx(ctx).
		Where("sub", sub).Where("product_id", productID).Count()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Transaction executes fn inside a database transaction.
// If fn returns an error the transaction is rolled back; otherwise committed.
func (r *PG) Transaction(ctx context.Context, fn func(ctx context.Context, tx gdb.TX) error) error {
	return r.db.Transaction(ctx, fn)
}

// UpdateOrderStatusTx sets the order status inside an existing transaction.
func (r *PG) UpdateOrderStatusTx(ctx context.Context, tx gdb.TX, orderID, status, providerTxID string, paidAt *gtime.Time) error {
	data := g.Map{
		"status":     status,
		"updated_at": gtime.Now(),
	}
	if providerTxID != "" {
		data["provider_tx_id"] = providerTxID
	}
	if paidAt != nil {
		data["paid_at"] = paidAt
	}
	_, err := tx.Ctx(ctx).Model("orders").Where("id", orderID).Data(data).Update()
	return err
}

// InsertEntitlementTx inserts an entitlement row inside an existing transaction.
// Duplicate (sub, product_id) is silently ignored (ON CONFLICT DO NOTHING).
func (r *PG) InsertEntitlementTx(ctx context.Context, tx gdb.TX, e *model.Entitlement) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	sql := `
INSERT INTO entitlements (id, sub, product_id, source, order_id, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (sub, product_id) DO NOTHING`
	var orderID interface{}
	if e.OrderID != nil {
		orderID = *e.OrderID
	}
	var expiresAt interface{}
	if e.ExpiresAt != nil {
		expiresAt = e.ExpiresAt
	}
	_, err := tx.Ctx(ctx).Exec(sql, e.ID, e.Sub, e.ProductID, e.Source, orderID, expiresAt)
	return err
}
