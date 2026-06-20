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
INSERT INTO products (id, site_key, external_id, kind, title, price_cents, points_cost, currency, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (site_key, external_id) DO UPDATE
  SET kind        = EXCLUDED.kind,
      title       = EXCLUDED.title,
      price_cents = EXCLUDED.price_cents,
      points_cost = EXCLUDED.points_cost,
      currency    = EXCLUDED.currency,
      status      = EXCLUDED.status,
      updated_at  = now()
RETURNING id`
	val, err := r.db.GetValue(ctx, sql,
		p.ID, p.SiteKey, p.ExternalID, p.Kind, p.Title, p.PriceCents, p.PointsCost, p.Currency, p.Status,
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

// EntitlementCount returns the number of entitlement rows for (sub, productID).
// Used in tests to assert exactly-once grant semantics.
func (r *PG) EntitlementCount(ctx context.Context, sub, productID string) (int, error) {
	return r.db.Model("entitlements").Ctx(ctx).
		Where("sub", sub).Where("product_id", productID).Count()
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

// ConditionalUpdateOrderStatusTx atomically transitions an order to newStatus only if
// its current status matches fromStatus. Returns (true, nil) when the row was updated,
// (false, nil) when no row matched (race lost or state already changed), and
// (false, err) on a database error.
func (r *PG) ConditionalUpdateOrderStatusTx(ctx context.Context, tx gdb.TX, orderID, fromStatus, toStatus, providerTxID string, paidAt *gtime.Time) (bool, error) {
	data := g.Map{
		"status":     toStatus,
		"updated_at": gtime.Now(),
	}
	if providerTxID != "" {
		data["provider_tx_id"] = providerTxID
	}
	if paidAt != nil {
		data["paid_at"] = paidAt
	}
	res, err := tx.Ctx(ctx).Model("orders").
		Where("id", orderID).Where("status", fromStatus).
		Data(data).Update()
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
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

// ─── M2: check-in + credits ─────────────────────────────────────────────────

// GetCheckin returns the check-in row for (sub, date YYYY-MM-DD), or (nil, nil).
func (r *PG) GetCheckin(ctx context.Context, sub, date string) (*model.CheckinRecord, error) {
	var rec *model.CheckinRecord
	if err := r.db.Model("checkin_records").Ctx(ctx).
		Where("sub", sub).Where("checkin_date", date).Limit(1).Scan(&rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// InsertCheckinTx inserts a check-in row inside a transaction. A same-day row
// (uq sub, checkin_date) is ignored; returns whether a row was actually inserted
// (false = a concurrent check-in won the race, caller must not double-earn).
func (r *PG) InsertCheckinTx(ctx context.Context, tx gdb.TX, rec *model.CheckinRecord) (bool, error) {
	sql := `
INSERT INTO checkin_records (id, sub, checkin_date, streak, points_awarded)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (sub, checkin_date) DO NOTHING`
	res, err := tx.Ctx(ctx).Exec(sql, uuid.NewString(), rec.Sub, rec.CheckinDate, rec.Streak, rec.PointsAwarded)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetBalance returns the subscriber's points balance (0 if no row).
func (r *PG) GetBalance(ctx context.Context, sub string) (int64, error) {
	v, err := r.db.Model("credits_balances").Ctx(ctx).
		Where("sub", sub).Fields("balance").Value()
	if err != nil {
		return 0, err
	}
	return v.Int64(), nil
}

// EarnCreditsTx credits delta points to sub inside a transaction: upserts the
// balance and appends a ledger row.
func (r *PG) EarnCreditsTx(ctx context.Context, tx gdb.TX, sub string, delta int, source, ref string) error {
	if _, err := tx.Ctx(ctx).Exec(`
INSERT INTO credits_balances (sub, balance) VALUES (?, ?)
ON CONFLICT (sub) DO UPDATE SET balance = credits_balances.balance + EXCLUDED.balance, updated_at = now()`,
		sub, delta); err != nil {
		return err
	}
	return r.insertLedgerTx(ctx, tx, sub, delta, source, ref)
}

// SpendCreditsTx debits amount points from sub inside a transaction, guarding
// against overspend: the balance is decremented only WHERE balance >= amount.
// Returns false (no ledger row written) when the balance is insufficient.
func (r *PG) SpendCreditsTx(ctx context.Context, tx gdb.TX, sub string, amount int, source, ref string) (bool, error) {
	res, err := tx.Ctx(ctx).Exec(`
UPDATE credits_balances SET balance = balance - ?, updated_at = now()
WHERE sub = ? AND balance >= ?`, amount, sub, amount)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil // insufficient balance (or no balance row)
	}
	if err := r.insertLedgerTx(ctx, tx, sub, -amount, source, ref); err != nil {
		return false, err
	}
	return true, nil
}

func (r *PG) insertLedgerTx(ctx context.Context, tx gdb.TX, sub string, delta int, source, ref string) error {
	_, err := tx.Ctx(ctx).Exec(
		`INSERT INTO credits_ledger (id, sub, delta, source, ref) VALUES (?, ?, ?, ?, ?)`,
		uuid.NewString(), sub, delta, source, ref)
	return err
}

// ListLedger returns a subscriber's ledger entries (newest first) plus the total.
func (r *PG) ListLedger(ctx context.Context, sub string, limit, offset int) ([]model.LedgerEntry, int, error) {
	m := r.db.Model("credits_ledger").Ctx(ctx).Where("sub", sub)
	total, err := m.Clone().Count()
	if err != nil {
		return nil, 0, err
	}
	var entries []model.LedgerEntry
	if err := m.Order("created_at DESC").Limit(offset, limit).Scan(&entries); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// GrantEntitlementTx inserts an entitlement inside a transaction, returning
// whether a row was actually inserted (false = the subscriber already owns it).
// The points-redemption path keys idempotency on this: it spends points only
// when the grant is fresh, so a repeat redeem never double-charges.
func (r *PG) GrantEntitlementTx(ctx context.Context, tx gdb.TX, e *model.Entitlement) (bool, error) {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	var orderID interface{}
	if e.OrderID != nil {
		orderID = *e.OrderID
	}
	res, err := tx.Ctx(ctx).Exec(`
INSERT INTO entitlements (id, sub, product_id, source, order_id)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (sub, product_id) DO NOTHING`, e.ID, e.Sub, e.ProductID, e.Source, orderID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
