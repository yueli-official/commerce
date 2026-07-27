// Package dao is the PostgreSQL data-access layer for the commerce service.
package dao

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/google/uuid"
	"github.com/yueli-official/foundation/go/audit"

	"github.com/yueli-official/commerce/internal/commerceaudit"
	"github.com/yueli-official/commerce/internal/deliveryrecovery"
	"github.com/yueli-official/commerce/internal/model"
	"github.com/yueli-official/commerce/internal/paymentrecovery"
	"github.com/yueli-official/commerce/internal/recoveryops"
)

// PG wraps the GoFrame gdb handle.
type PG struct {
	db    gdb.DB
	audit *commerceaudit.Journal
}

// NewPG constructs a PG repo from a GoFrame DB handle.
func NewPG(db gdb.DB) *PG { return &PG{db: db} }

func (r *PG) UseAudit(journal *commerceaudit.Journal) {
	r.audit = journal
}

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
		"id":           o.ID,
		"order_no":     o.OrderNo,
		"sub":          o.Sub,
		"product_id":   o.ProductID,
		"amount_cents": o.AmountCents,
		"currency":     o.Currency,
		"status":       o.Status,
		"gateway":      o.Gateway,
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

func (r *PG) GetOrderByID(ctx context.Context, id string) (*model.Order, error) {
	var o *model.Order
	if err := r.db.Model("orders").Ctx(ctx).Where("id", id).Limit(1).Scan(&o); err != nil {
		return nil, err
	}
	return o, nil
}

func (r *PG) ListOrders(ctx context.Context, status, q string, limit, offset int) ([]*model.Order, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	m := r.orderListModel(ctx, status, q)
	total, err := r.orderListModel(ctx, status, q).Count()
	if err != nil {
		return nil, 0, err
	}
	var orders []*model.Order
	if err := m.Order("created_at DESC").Limit(offset, limit).Scan(&orders); err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func (r *PG) orderListModel(ctx context.Context, status, q string) *gdb.Model {
	m := r.db.Model("orders").Ctx(ctx)
	if status != "" {
		m = m.Where("status", status)
	}
	if q != "" {
		like := "%" + q + "%"
		m = m.Where("(order_no LIKE ? OR buyer_email LIKE ?)", like, like)
	}
	return m
}

// UpdateOrderStatus sets the status (and optionally paid_at) on an order.
func (r *PG) UpdateOrderStatus(ctx context.Context, orderID, status string) error {
	_, err := r.db.Model("orders").Ctx(ctx).
		Where("id", orderID).
		Data(g.Map{"status": status, "updated_at": gtime.Now()}).
		Update()
	return err
}

// UpsertBuyer resolves a logged-in or guest buyer for checkout orders.
func (r *PG) UpsertBuyer(ctx context.Context, b *model.Buyer) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.Kind == model.BuyerKindUser {
		sql := `
INSERT INTO commerce_buyers (id, kind, buyer_sub, buyer_email, email_normalized)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (buyer_sub) WHERE buyer_sub IS NOT NULL DO UPDATE
  SET buyer_email = EXCLUDED.buyer_email,
      email_normalized = EXCLUDED.email_normalized,
      updated_at = now()
RETURNING id`
		val, err := r.db.GetValue(ctx, sql, b.ID, b.Kind, b.BuyerSub, b.BuyerEmail, b.EmailNormalized)
		if err != nil {
			return err
		}
		b.ID = val.String()
		return nil
	}
	_, err := r.db.Exec(ctx, `
INSERT INTO commerce_buyers (id, kind, buyer_email, email_normalized)
VALUES (?, ?, ?, ?)`, b.ID, b.Kind, b.BuyerEmail, b.EmailNormalized)
	return err
}

// InsertCheckoutOrder inserts a checkout order and its item snapshots.
func (r *PG) InsertCheckoutOrder(ctx context.Context, o *model.Order, items []*model.OrderItem) error {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	return r.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		return r.InsertCheckoutOrderTx(ctx, tx, o, items)
	})
}

func (r *PG) FindReusableCheckout(ctx context.Context, buyerSub, buyerEmail, provider, variantID string, amountCents int, currency string, since time.Time) (*model.Order, error) {
	var o *model.Order
	err := r.db.Model("orders o").Ctx(ctx).
		InnerJoin("order_items oi", "oi.order_id = o.id").
		Where("o.status", model.OrderStatusPaying).
		Where("o.payment_provider", provider).
		Where("oi.variant_id", variantID).
		Where("o.amount_cents", amountCents).
		Where("o.currency", currency).
		Where("o.created_at >= ?", since).
		Where("((? <> '' AND o.buyer_sub = ?) OR (? <> '' AND lower(o.buyer_email) = ?))",
			buyerSub, buyerSub, buyerEmail, buyerEmail).
		Order("o.created_at DESC").
		Limit(1).
		Fields("o.*").
		Scan(&o)
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (r *PG) CompletedCheckoutQuantityByVariant(ctx context.Context, buyerSub, buyerEmail, variantID string) (int, error) {
	val, err := r.db.GetValue(ctx, `
SELECT COALESCE(SUM(oi.quantity), 0)
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
WHERE oi.variant_id = ?
  AND o.status IN (?, ?)
  AND ((? <> '' AND o.buyer_sub = ?) OR (? <> '' AND lower(o.buyer_email) = ?))`,
		variantID,
		model.OrderStatusPaid,
		model.OrderStatusFulfilled,
		buyerSub,
		buyerSub,
		buyerEmail,
		buyerEmail,
	)
	if err != nil {
		return 0, err
	}
	return val.Int(), nil
}

func (r *PG) InsertCheckoutOrderTx(ctx context.Context, tx gdb.TX, o *model.Order, items []*model.OrderItem) error {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	_, err := tx.Ctx(ctx).Exec(`
INSERT INTO orders (
    id, order_no, sub, product_id, amount_cents, currency, status, gateway,
    buyer_id, buyer_sub, buyer_email, payment_provider, payment_session_id,
    return_url, cancel_url, delivery_state
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.OrderNo, nullableString(o.Sub), o.ProductID, o.AmountCents, o.Currency, o.Status, o.Gateway,
		nullableString(o.BuyerID), nullableString(o.BuyerSub), nullableString(o.BuyerEmail), o.PaymentProvider, nullableString(o.PaymentSessionID),
		o.ReturnURL, o.CancelURL, o.DeliveryState,
	)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == "" {
			item.ID = uuid.NewString()
		}
		item.OrderID = o.ID
		if _, err := tx.Ctx(ctx).Exec(`
INSERT INTO order_items (
    id, order_id, site_key, external_id, product_id, variant_id, title_snapshot,
    variant_title_snapshot, sku_snapshot, quantity, unit_price_cents, unit_points_cost,
    currency, delivery_kind_snapshot, delivery_ref_snapshot
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.OrderID, item.SiteKey, item.ExternalID, nullableString(item.ProductID), item.VariantID, item.TitleSnapshot,
			item.VariantTitleSnapshot, item.SKUSnapshot, item.Quantity, item.UnitPriceCents, item.UnitPointsCost,
			item.Currency, item.DeliveryKindSnapshot, item.DeliveryRefSnapshot,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *PG) OrderItems(ctx context.Context, orderID string) ([]*model.OrderItem, error) {
	var items []*model.OrderItem
	if err := r.db.Model("order_items").Ctx(ctx).Where("order_id", orderID).Order("created_at ASC").Scan(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *PG) OrderItemsTx(ctx context.Context, tx gdb.TX, orderID string) ([]*model.OrderItem, error) {
	var items []*model.OrderItem
	err := tx.Ctx(ctx).Model("order_items").
		Where("order_id", orderID).
		Order("created_at ASC").
		Scan(&items)
	return items, err
}

func (r *PG) OrderItemByID(ctx context.Context, id string) (*model.OrderItem, error) {
	var item *model.OrderItem
	if err := r.db.Model("order_items").Ctx(ctx).Where("id", id).Limit(1).Scan(&item); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *PG) UpdatePaymentSession(ctx context.Context, orderID, sessionID string) error {
	_, err := r.db.Exec(ctx, `
UPDATE orders
SET payment_session_id = ?, updated_at = now()
WHERE id = ?`, nullableString(sessionID), orderID)
	return err
}

func (r *PG) UpdatePaymentSessionTx(
	ctx context.Context,
	tx gdb.TX,
	orderID, sessionID string,
) error {
	_, err := tx.Ctx(ctx).Exec(`
UPDATE orders
SET payment_session_id = ?, updated_at = now()
WHERE id = ?`, nullableString(sessionID), orderID)
	return err
}

func (r *PG) PaymentMethods(ctx context.Context) ([]*model.PaymentMethod, error) {
	var methods []*model.PaymentMethod
	err := r.db.Model("commerce_payment_methods").Ctx(ctx).
		Order("sort_order ASC, provider ASC").
		Scan(&methods)
	return methods, err
}

func (r *PG) UpsertPaymentMethod(ctx context.Context, method *model.PaymentMethod) error {
	_, err := r.db.Exec(ctx, `
INSERT INTO commerce_payment_methods (provider, enabled, display_name, sort_order)
VALUES (?, ?, ?, ?)
ON CONFLICT (provider) DO UPDATE
  SET enabled = EXCLUDED.enabled,
      display_name = EXCLUDED.display_name,
      sort_order = EXCLUDED.sort_order,
      updated_at = now()`,
		method.Provider, method.Enabled, method.DisplayName, method.SortOrder,
	)
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
		Where("sub", sub).Where("product_id", productID).Where("state", "active").Count()
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

func (r *PG) FulfillCheckoutTx(ctx context.Context, tx gdb.TX, orderID, providerTxID string, paidAt *gtime.Time) (bool, error) {
	data := g.Map{
		"status":         model.OrderStatusFulfilled,
		"delivery_state": model.DeliveryStateGranted,
		"fulfilled_at":   paidAt,
		"updated_at":     gtime.Now(),
		"provider_tx_id": providerTxID,
		"paid_at":        paidAt,
	}
	res, err := tx.Ctx(ctx).Model("orders").Where("id", orderID).Where("status", model.OrderStatusPaying).Data(data).Update()
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *PG) InsertPaymentEventTx(ctx context.Context, tx gdb.TX, event *model.PaymentEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	_, err := tx.Ctx(ctx).Exec(`
INSERT INTO payment_events (id, order_id, provider, event_type, provider_event_id, raw_hash, amount_cents, success, message)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, nullableString(event.OrderID), event.Provider, event.EventType, event.ProviderEventID, event.RawHash,
		event.AmountCents, event.Success, event.Message,
	)
	return err
}

// ReserveProviderEventTx appends immutable verified evidence. The unique
// provider/merchant/idempotency key serializes concurrent replays. The caller
// must compare SameEvidence when inserted is false.
func (r *PG) ReserveProviderEventTx(
	ctx context.Context,
	tx gdb.TX,
	event *paymentrecovery.ProviderEvent,
) (*paymentrecovery.ProviderEvent, bool, error) {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	result, err := tx.Ctx(ctx).Exec(`
INSERT INTO provider_events (
    id, provider, merchant_account, source, operation, idempotency_key,
    provider_event_id, payload_digest, provider_status, normalized_status,
    order_no, provider_object_id, order_id, payment_attempt_id, refund_id,
    dispute_id, amount_cents, currency, occurred_at, processing_state
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, merchant_account, idempotency_key) DO NOTHING`,
		event.ID, event.Provider, event.Merchant, event.Source, event.Operation,
		event.IdempotencyKey, event.ProviderEventID, event.PayloadDigest,
		event.ProviderStatus, event.NormalizedStatus, event.OrderNo,
		event.ProviderObjectID, nullableString(event.OrderID),
		nullableString(event.PaymentAttemptID), nullableString(event.RefundID),
		nullableString(event.DisputeID), event.AmountCents, event.Currency,
		event.OccurredAt, event.Processing,
	)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if affected > 0 {
		copy := *event
		return &copy, true, nil
	}
	var existing *paymentrecovery.ProviderEvent
	err = tx.Ctx(ctx).Model("provider_events").
		Where("provider", event.Provider).
		Where("merchant_account", event.Merchant).
		Where("idempotency_key", event.IdempotencyKey).
		LockUpdate().
		Limit(1).
		Scan(&existing)
	if err != nil {
		return nil, false, err
	}
	return existing, false, nil
}

func (r *PG) FinalizeProviderEventTx(
	ctx context.Context,
	tx gdb.TX,
	eventID, orderID, paymentAttemptID string,
	state paymentrecovery.ProcessingState,
	processingError string,
) error {
	data := g.Map{
		"processing_state": state,
		"processing_error": strings.TrimSpace(processingError),
		"processed_at":     gtime.Now(),
	}
	if orderID != "" {
		data["order_id"] = orderID
	}
	if paymentAttemptID != "" {
		data["payment_attempt_id"] = paymentAttemptID
	}
	_, err := tx.Ctx(ctx).Model("provider_events").Where("id", eventID).Data(data).Update()
	return err
}

func (r *PG) FinalizeDisputeProviderEventTx(
	ctx context.Context,
	tx gdb.TX,
	eventID, orderID, disputeID string,
	state paymentrecovery.ProcessingState,
	processingError string,
) error {
	data := g.Map{
		"processing_state": state,
		"processing_error": strings.TrimSpace(processingError),
		"processed_at":     gtime.Now(),
	}
	if orderID != "" {
		data["order_id"] = orderID
	}
	if disputeID != "" {
		data["dispute_id"] = disputeID
	}
	_, err := tx.Ctx(ctx).Model("provider_events").
		Where("id", eventID).
		Data(data).
		Update()
	return err
}

func (r *PG) ProviderEventByKey(
	ctx context.Context,
	provider, merchant, idempotencyKey string,
) (*paymentrecovery.ProviderEvent, error) {
	var event *paymentrecovery.ProviderEvent
	err := r.db.Model("provider_events").Ctx(ctx).
		Where("provider", provider).
		Where("merchant_account", merchant).
		Where("idempotency_key", idempotencyKey).
		Limit(1).
		Scan(&event)
	return event, err
}

func (r *PG) GetOrderByNoTxForUpdate(ctx context.Context, tx gdb.TX, orderNo string) (*model.Order, error) {
	var order *model.Order
	err := tx.Ctx(ctx).Model("orders").
		Where("order_no", orderNo).
		LockUpdate().
		Limit(1).
		Scan(&order)
	return order, err
}

func (r *PG) GetOrderByProviderTxTxForUpdate(
	ctx context.Context,
	tx gdb.TX,
	provider, providerTxID string,
) (*model.Order, error) {
	var order *model.Order
	err := tx.Ctx(ctx).Model("orders").
		Where("payment_provider", provider).
		Where("provider_tx_id", providerTxID).
		LockUpdate().
		Limit(1).
		Scan(&order)
	return order, err
}

func (r *PG) PaymentAttemptIDByProviderTxTx(
	ctx context.Context,
	tx gdb.TX,
	provider, merchant, providerTxID string,
) (string, error) {
	var rows []struct {
		ID string `orm:"id"`
	}
	err := tx.Ctx(ctx).Model("payment_attempts").
		Fields("id").
		Where("provider", provider).
		Where("merchant_account", merchant).
		Where("provider_tx_id", providerTxID).
		Limit(1).
		Scan(&rows)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].ID, nil
}

func (r *PG) EnsurePaymentAttemptTx(
	ctx context.Context,
	tx gdb.TX,
	order *model.Order,
	provider, merchant string,
) (*paymentrecovery.PaymentAttemptRecord, error) {
	idempotencyKey := "order:" + order.ID
	_, err := tx.Ctx(ctx).Exec(`
INSERT INTO payment_attempts (
    id, order_id, provider, merchant_account, idempotency_key, status,
    amount_cents, currency, provider_session_id
)
VALUES (?, ?, ?, ?, ?, 'created', ?, ?, ?)
ON CONFLICT (provider, merchant_account, idempotency_key) DO NOTHING`,
		uuid.NewString(), order.ID, provider, merchant, idempotencyKey,
		order.AmountCents, strings.ToUpper(order.Currency), order.PaymentSessionID,
	)
	if err != nil {
		return nil, err
	}
	var attempt *paymentrecovery.PaymentAttemptRecord
	err = tx.Ctx(ctx).Model("payment_attempts").
		Where("provider", provider).
		Where("merchant_account", merchant).
		Where("idempotency_key", idempotencyKey).
		LockUpdate().
		Limit(1).
		Scan(&attempt)
	return attempt, err
}

func (r *PG) UpdatePaymentAttemptSessionTx(
	ctx context.Context,
	tx gdb.TX,
	orderID, provider, merchant, sessionID string,
) error {
	_, err := tx.Ctx(ctx).Model("payment_attempts").
		Where("order_id", orderID).
		Where("provider", provider).
		Where("merchant_account", merchant).
		Data(g.Map{
			"provider_session_id": strings.TrimSpace(sessionID),
			"updated_at":          gtime.Now(),
		}).
		Update()
	return err
}

func (r *PG) UpdatePaymentAttemptTx(
	ctx context.Context,
	tx gdb.TX,
	attemptID string,
	payment paymentrecovery.Payment,
) error {
	_, err := tx.Ctx(ctx).Model("payment_attempts").Where("id", attemptID).Data(g.Map{
		"status":           payment.Status,
		"provider_tx_id":   payment.ProviderTxID,
		"revision":         payment.Revision,
		"last_observed_at": payment.LastObservedAt,
		"updated_at":       gtime.Now(),
	}).Update()
	return err
}

func (r *PG) RecordPaymentReconciledTx(
	ctx context.Context,
	tx gdb.TX,
	attemptID string,
	status paymentrecovery.PaymentStatus,
	observedAt time.Time,
) error {
	data := g.Map{
		"last_reconciled_at":      observedAt.UTC(),
		"reconciliation_failures": 0,
		"reconciliation_error":    "",
		"updated_at":              gtime.Now(),
	}
	switch status {
	case paymentrecovery.PaymentCreated,
		paymentrecovery.PaymentActionRequired,
		paymentrecovery.PaymentPending:
		data["next_reconcile_at"] = observedAt.UTC().Add(5 * time.Minute)
	default:
		data["next_reconcile_at"] = nil
	}
	_, err := tx.Ctx(ctx).Model("payment_attempts").Where("id", attemptID).Data(data).Update()
	return err
}

func (r *PG) DuePaymentAttempts(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]paymentrecovery.DuePaymentAttempt, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var attempts []paymentrecovery.DuePaymentAttempt
	err := r.db.Model("payment_attempts pa").Ctx(ctx).
		Fields("pa.id, o.order_no, pa.provider, pa.next_reconcile_at").
		InnerJoin("orders o", "o.id = pa.order_id").
		WhereIn("pa.status", []string{
			string(paymentrecovery.PaymentCreated),
			string(paymentrecovery.PaymentActionRequired),
			string(paymentrecovery.PaymentPending),
		}).
		WhereNotNull("pa.next_reconcile_at").
		WhereLTE("pa.next_reconcile_at", now.UTC()).
		OrderAsc("pa.next_reconcile_at").
		Limit(limit).
		Scan(&attempts)
	return attempts, err
}

func (r *PG) DeferPaymentReconciliation(
	ctx context.Context,
	attemptID string,
	from, next time.Time,
) (bool, error) {
	result, err := r.db.Model("payment_attempts").Ctx(ctx).
		Where("id", attemptID).
		Where("next_reconcile_at", from.UTC()).
		Data(g.Map{
			"next_reconcile_at": next.UTC(),
			"updated_at":        gtime.Now(),
		}).
		Update()
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (r *PG) UpdateOrderPaymentStateTx(
	ctx context.Context,
	tx gdb.TX,
	orderID string,
	status paymentrecovery.PaymentStatus,
) error {
	_, err := tx.Ctx(ctx).Model("orders").Where("id", orderID).Data(g.Map{
		"payment_state": status,
		"updated_at":    gtime.Now(),
	}).Update()
	return err
}

// FulfillRecoveredCheckoutTx lets authoritative provider evidence repair a
// locally cancelled order. A local cancel never cancels provider money.
func (r *PG) FulfillRecoveredCheckoutTx(
	ctx context.Context,
	tx gdb.TX,
	orderID, providerTxID string,
	paidAt *gtime.Time,
) (bool, error) {
	result, err := tx.Ctx(ctx).Model("orders").
		Where("id", orderID).
		WhereIn("status", []string{
			model.OrderStatusPending,
			model.OrderStatusPaying,
			model.OrderStatusCancelled,
		}).
		Data(g.Map{
			"status":         model.OrderStatusFulfilled,
			"payment_state":  paymentrecovery.PaymentSettled,
			"delivery_state": model.DeliveryStateGranted,
			"fulfilled_at":   paidAt,
			"updated_at":     gtime.Now(),
			"provider_tx_id": providerTxID,
			"paid_at":        paidAt,
		}).
		Update()
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (r *PG) InsertDeliveryGrantTx(ctx context.Context, tx gdb.TX, grant *model.DeliveryGrant) error {
	if grant.ID == "" {
		grant.ID = uuid.NewString()
	}
	_, err := tx.Ctx(ctx).Exec(`
INSERT INTO delivery_grants (id, order_id, order_item_id, buyer_sub, buyer_email, token_hash, delivery_ref, state, expires_at, max_downloads, download_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		grant.ID, grant.OrderID, nullableString(grant.OrderItemID), nullableString(grant.BuyerSub), nullableString(grant.BuyerEmail),
		grant.TokenHash, grant.DeliveryRef, grant.State, grant.ExpiresAt, grant.MaxDownloads, grant.DownloadCount,
	)
	return err
}

func (r *PG) RecordDeliveryDownload(ctx context.Context, grantID string, maxDownloads int) (bool, error) {
	if maxDownloads <= 0 {
		_, err := r.db.Exec(ctx, `
UPDATE delivery_grants
   SET download_count = download_count + 1
 WHERE id = ? AND state = 'active'`, grantID)
		return true, err
	}
	res, err := r.db.Exec(ctx, `
UPDATE delivery_grants
   SET download_count = download_count + 1
 WHERE id = ?
   AND state = 'active'
   AND download_count < ?`, grantID, maxDownloads)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *PG) RollbackDeliveryDownload(ctx context.Context, grantID string) error {
	_, err := r.db.Exec(ctx, `
UPDATE delivery_grants
   SET download_count = GREATEST(download_count - 1, 0)
 WHERE id = ?`, grantID)
	return err
}

func (r *PG) DeliveryGrantByTokenHash(ctx context.Context, tokenHash string) (*model.DeliveryGrant, error) {
	var grant *model.DeliveryGrant
	err := r.db.Model("delivery_grants").Ctx(ctx).
		Where("token_hash", tokenHash).
		Where("state", "active").
		Where("(expires_at IS NULL OR expires_at > now())").
		Limit(1).
		Scan(&grant)
	if err != nil {
		return nil, err
	}
	return grant, nil
}

func (r *PG) DeliveryGrantsByBuyerSub(ctx context.Context, sub, q, state string, limit, offset int) ([]*model.DeliveryGrant, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	m := r.deliveryGrantsByBuyerSubModel(ctx, sub, q, state)
	total, err := r.deliveryGrantsByBuyerSubModel(ctx, sub, q, state).Count()
	if err != nil {
		return nil, 0, err
	}
	var grants []*model.DeliveryGrant
	err = m.Order("g.created_at DESC").
		Limit(offset, limit).
		Scan(&grants)
	return grants, total, err
}

func (r *PG) deliveryGrantsByBuyerSubModel(ctx context.Context, sub, q, state string) *gdb.Model {
	m := r.db.Model("delivery_grants g").Ctx(ctx).
		Fields("g.*").
		LeftJoin("orders o", "o.id = g.order_id").
		LeftJoin("order_items oi", "oi.id = g.order_item_id").
		Where("g.buyer_sub", sub)
	state = strings.TrimSpace(state)
	switch state {
	case "":
		// All purchase grants for the buyer.
	case "active":
		m = m.Where("g.state", "active").Where("(g.expires_at IS NULL OR g.expires_at > now())")
	case "expired":
		m = m.Where("g.state", "active").Where("g.expires_at IS NOT NULL AND g.expires_at <= now()")
	default:
		m = m.Where("g.state", state)
	}
	q = strings.TrimSpace(q)
	if q != "" {
		like := "%" + q + "%"
		m = m.Where("(o.order_no LIKE ? OR oi.title_snapshot LIKE ? OR oi.variant_title_snapshot LIKE ? OR oi.sku_snapshot LIKE ? OR g.delivery_ref LIKE ?)", like, like, like, like, like)
	}
	return m
}

func (r *PG) DeliveryGrantsByOrderID(ctx context.Context, orderID string) ([]*model.DeliveryGrant, error) {
	var grants []*model.DeliveryGrant
	err := r.db.Model("delivery_grants").Ctx(ctx).
		Where("order_id", orderID).
		Order("created_at DESC").
		Scan(&grants)
	return grants, err
}

func (r *PG) PaymentEventsByOrderID(ctx context.Context, orderID string) ([]*model.PaymentEvent, error) {
	var events []*model.PaymentEvent
	err := r.db.Model("payment_events").Ctx(ctx).
		Where("order_id", orderID).
		Order("created_at DESC").
		Scan(&events)
	return events, err
}

func (r *PG) RevokeDeliveryGrantsByOrderIDTx(ctx context.Context, tx gdb.TX, orderID string) (int64, error) {
	res, err := tx.Ctx(ctx).Exec(`
UPDATE delivery_grants
   SET state = 'revoked', revoked_at = now(),
       suspended_at = NULL, suspended_reason = ''
 WHERE order_id = ? AND state IN ('active', 'suspended')`, orderID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := r.QueueAssetGrantRevocationsByOrderIDTx(ctx, tx, orderID); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *PG) RecordAssetDeliveryGrant(
	ctx context.Context,
	grant deliveryrecovery.Grant,
) (*deliveryrecovery.Grant, error) {
	if grant.ID == "" {
		grant.ID = uuid.NewString()
	}
	_, err := r.db.Exec(ctx, `
INSERT INTO asset_delivery_grants (
    id, order_id, delivery_grant_id, asset_id, provider_grant_id,
    state, expires_at, next_revoke_at
)
SELECT ?, dg.order_id, dg.id, ?, ?,
       CASE WHEN dg.state = 'active' THEN 'active' ELSE 'revoke_pending' END,
       ?,
       CASE WHEN dg.state = 'active' THEN NULL ELSE now() END
FROM delivery_grants dg
WHERE dg.id = ?
ON CONFLICT (provider_grant_id) DO NOTHING`,
		grant.ID, grant.AssetID, grant.ProviderGrantID, grant.ExpiresAt, grant.DeliveryGrantID,
	)
	if err != nil {
		return nil, err
	}
	var stored *deliveryrecovery.Grant
	err = r.db.Model("asset_delivery_grants").Ctx(ctx).
		Where("provider_grant_id", grant.ProviderGrantID).
		Limit(1).
		Scan(&stored)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("delivery grant %s no longer exists", grant.DeliveryGrantID)
	}
	return stored, nil
}

func (r *PG) QueueAssetGrantRevocationsByOrderIDTx(
	ctx context.Context,
	tx gdb.TX,
	orderID string,
) (int64, error) {
	result, err := tx.Ctx(ctx).Exec(`
UPDATE asset_delivery_grants
   SET state = 'revoke_pending',
       next_revoke_at = now(),
       updated_at = now()
 WHERE order_id = ?
   AND state = 'active'
   AND expires_at > now()`, orderID)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return affected, err
	}
	if err := r.appendRecoveryAudit(
		ctx, tx, commerceaudit.ActionAccessRevocationQueued,
		audit.Target{Type: "commerce.order", ID: orderID},
		commerceaudit.Evidence{
			State: deliveryrecovery.StateRevokePending, Count: uint64(affected),
		},
	); err != nil {
		return 0, err
	}
	return affected, nil
}

func (r *PG) DueAssetGrantRevocations(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]deliveryrecovery.Grant, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if _, err := r.db.Exec(ctx, `
UPDATE asset_delivery_grants
   SET state = 'expired', next_revoke_at = NULL, updated_at = now()
 WHERE state IN ('active', 'revoke_pending') AND expires_at <= ?`, now.UTC()); err != nil {
		return nil, err
	}
	var grants []deliveryrecovery.Grant
	err := r.db.Model("asset_delivery_grants").Ctx(ctx).
		Where("state", deliveryrecovery.StateRevokePending).
		Where("next_revoke_at <= ?", now.UTC()).
		Order("next_revoke_at ASC").
		Limit(limit).
		Scan(&grants)
	return grants, err
}

func (r *PG) DeferAssetGrantRevocation(
	ctx context.Context,
	id string,
	due time.Time,
	next time.Time,
) (bool, error) {
	result, err := r.db.Model("asset_delivery_grants").Ctx(ctx).
		Where("id", id).
		Where("state", deliveryrecovery.StateRevokePending).
		Where("next_revoke_at", due.UTC()).
		Data(g.Map{"next_revoke_at": next.UTC(), "updated_at": gtime.Now()}).
		Update()
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (r *PG) AssetDeliveryGrant(ctx context.Context, id string) (*deliveryrecovery.Grant, error) {
	var grant *deliveryrecovery.Grant
	err := r.db.Model("asset_delivery_grants").Ctx(ctx).
		Where("id", id).
		Limit(1).
		Scan(&grant)
	return grant, err
}

func (r *PG) MarkAssetGrantRevoked(ctx context.Context, id string) error {
	return r.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var grant *deliveryrecovery.Grant
		if err := tx.Ctx(ctx).Model("asset_delivery_grants").
			Where("id", id).Limit(1).Scan(&grant); err != nil {
			return err
		}
		if grant == nil {
			return nil
		}
		if _, err := tx.Ctx(ctx).Model("asset_delivery_grants").
			Where("id", id).
			WhereIn("state", []string{deliveryrecovery.StateActive, deliveryrecovery.StateRevokePending}).
			Data(g.Map{
				"state": deliveryrecovery.StateRevoked, "revoked_at": gtime.Now(),
				"next_revoke_at": nil, "last_error": "", "updated_at": gtime.Now(),
			}).
			Update(); err != nil {
			return err
		}
		return r.appendRecoveryAudit(
			ctx, tx, commerceaudit.ActionRemoteGrantRevoked,
			audit.Target{Type: "commerce.delivery_grant", ID: id},
			commerceaudit.Evidence{
				State:           deliveryrecovery.StateRevoked,
				ProviderGrantID: grant.ProviderGrantID,
				Attempts:        uint64(grant.RevokeAttempts),
			},
		)
	})
}

func (r *PG) MarkAssetGrantRevokeFailed(ctx context.Context, id, message string) (int, error) {
	if len(message) > 1000 {
		message = message[:1000]
	}
	var attempts int
	err := r.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := tx.Ctx(ctx).Exec(`
UPDATE asset_delivery_grants
   SET revoke_attempts = revoke_attempts + 1,
       last_error = ?,
       updated_at = now()
 WHERE id = ? AND state = 'revoke_pending'`, message, id); err != nil {
			return err
		}
		return tx.Ctx(ctx).GetScan(&attempts, `
SELECT revoke_attempts FROM asset_delivery_grants WHERE id = ?`, id)
	})
	return attempts, err
}

func (r *PG) ListAssetGrantRevocations(
	ctx context.Context,
	state string,
	limit int,
	offset int,
) ([]deliveryrecovery.Grant, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := r.db.Model("asset_delivery_grants").Ctx(ctx)
	if state = strings.TrimSpace(state); state != "" {
		query = query.Where("state", state)
	}
	total, err := query.Clone().Count()
	if err != nil {
		return nil, 0, err
	}
	var grants []deliveryrecovery.Grant
	err = query.Order("created_at DESC").Limit(offset, limit).Scan(&grants)
	return grants, total, err
}

func (r *PG) RetryAssetGrantRevocation(ctx context.Context, id string) (bool, error) {
	queued := false
	err := r.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var grant *deliveryrecovery.Grant
		if err := tx.Ctx(ctx).Model("asset_delivery_grants").
			Where("id", id).Limit(1).Scan(&grant); err != nil {
			return err
		}
		if grant == nil || grant.State != deliveryrecovery.StateRevokePending {
			return nil
		}
		result, err := tx.Ctx(ctx).Model("asset_delivery_grants").
			Where("id", id).
			Where("state", deliveryrecovery.StateRevokePending).
			Data(g.Map{"next_revoke_at": gtime.Now(), "updated_at": gtime.Now()}).
			Update()
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected == 0 {
			return err
		}
		queued = true
		return r.appendRecoveryAudit(
			ctx, tx, commerceaudit.ActionRecoveryRetried,
			audit.Target{Type: "commerce.delivery_grant", ID: id},
			commerceaudit.Evidence{
				State: grant.State, ProviderGrantID: grant.ProviderGrantID,
				Attempts: uint64(grant.RevokeAttempts),
			},
		)
	})
	return queued, err
}

func (r *PG) ListRecoveryCases(
	ctx context.Context,
	kind string,
	state string,
	limit int,
	offset int,
) ([]recoveryops.Case, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	kind, state = strings.TrimSpace(kind), strings.TrimSpace(state)
	const casesSQL = `
WITH recovery_cases AS (
    SELECT 'payment'::text AS kind, pa.id::text, pa.order_id::text,
           o.order_no, pa.provider, pa.status::text AS state,
           pa.reconciliation_failures AS attempts,
           pa.reconciliation_error AS last_error,
           pa.next_reconcile_at AS next_action_at,
           pa.created_at, pa.updated_at
      FROM payment_attempts pa
      JOIN orders o ON o.id = pa.order_id
     WHERE pa.status IN ('created', 'action_required', 'pending', 'failed')
        OR pa.reconciliation_failures > 0
    UNION ALL
    SELECT 'refund'::text, r.id::text, r.order_id::text,
           o.order_no, r.provider, r.status::text,
           r.reconciliation_failures, r.reconciliation_error,
           r.next_reconcile_at, r.created_at, r.updated_at
      FROM refunds r
      JOIN orders o ON o.id = r.order_id
     WHERE r.status IN ('requested', 'submitting', 'pending', 'failed')
        OR r.reconciliation_failures > 0
    UNION ALL
    SELECT 'dispute'::text, d.id::text, COALESCE(d.order_id::text, ''),
           COALESCE(o.order_no, ''), d.provider, d.status::text,
           0, '', d.due_at, d.created_at, d.updated_at
      FROM disputes d
      LEFT JOIN orders o ON o.id = d.order_id
     WHERE d.status IN ('open', 'needs_response', 'under_review')
    UNION ALL
    SELECT 'asset_grant'::text, ag.id::text, ag.order_id::text,
           o.order_no, 'asset'::text, ag.state::text,
           ag.revoke_attempts, ag.last_error, ag.next_revoke_at,
           ag.created_at, ag.updated_at
      FROM asset_delivery_grants ag
      JOIN orders o ON o.id = ag.order_id
     WHERE ag.state = 'revoke_pending'
)
`
	filter := ` WHERE (? = '' OR kind = ?) AND (? = '' OR state = ?)`
	value, err := r.db.GetValue(ctx, casesSQL+`SELECT count(*) FROM recovery_cases`+filter,
		kind, kind, state, state)
	if err != nil {
		return nil, 0, err
	}
	var cases []recoveryops.Case
	err = r.db.GetScan(ctx, &cases, casesSQL+`
SELECT * FROM recovery_cases`+filter+`
 ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`,
		kind, kind, state, state, limit, offset)
	return cases, value.Int(), err
}

func (r *PG) RetryRecoveryCase(ctx context.Context, kind, id string) (bool, error) {
	kind, id = strings.TrimSpace(kind), strings.TrimSpace(id)
	if kind == recoveryops.KindAssetGrant {
		return r.RetryAssetGrantRevocation(ctx, id)
	}
	var table, nextColumn, targetType string
	var states []string
	switch kind {
	case recoveryops.KindPayment:
		table, nextColumn, targetType = "payment_attempts", "next_reconcile_at", "commerce.payment_attempt"
		states = []string{"created", "action_required", "pending", "failed"}
	case recoveryops.KindRefund:
		table, nextColumn, targetType = "refunds", "next_reconcile_at", "commerce.refund"
		states = []string{"requested", "submitting", "pending", "failed"}
	default:
		return false, nil
	}
	queued := false
	err := r.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var current struct {
			State    string `orm:"state"`
			Attempts int    `orm:"attempts"`
		}
		if err := tx.Ctx(ctx).GetScan(
			&current,
			fmt.Sprintf(
				"SELECT status AS state, reconciliation_failures AS attempts FROM %s WHERE id = ?",
				table,
			),
			id,
		); err != nil {
			return err
		}
		result, err := tx.Ctx(ctx).Model(table).
			Where("id", id).WhereIn("status", states).
			Data(g.Map{nextColumn: gtime.Now(), "updated_at": gtime.Now()}).
			Update()
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected == 0 {
			return err
		}
		queued = true
		return r.appendRecoveryAudit(
			ctx, tx, commerceaudit.ActionRecoveryRetried,
			audit.Target{Type: targetType, ID: id},
			commerceaudit.Evidence{
				State: current.State, Attempts: uint64(current.Attempts),
			},
		)
	})
	return queued, err
}

func (r *PG) MarkOrderRefundedTx(ctx context.Context, tx gdb.TX, orderID, providerRefundID string) error {
	data := g.Map{
		"status":         model.OrderStatusRefunded,
		"delivery_state": model.DeliveryStateRevoked,
		"updated_at":     gtime.Now(),
	}
	_, err := tx.Ctx(ctx).Model("orders").Where("id", orderID).Data(data).Update()
	return err
}

func (r *PG) CreateRefundTx(
	ctx context.Context,
	tx gdb.TX,
	refund *paymentrecovery.RefundRecord,
) (*paymentrecovery.RefundRecord, bool, error) {
	if refund.ID == "" {
		refund.ID = uuid.NewString()
	}
	result, err := tx.Ctx(ctx).Exec(`
INSERT INTO refunds (
    id, order_id, payment_attempt_id, provider, merchant_account,
    refund_no, idempotency_key, amount_cents, currency, reason,
    status, requested_by
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, merchant_account, idempotency_key) DO NOTHING`,
		refund.ID, refund.OrderID, nullableString(refund.PaymentAttemptID),
		refund.Provider, refund.Merchant, refund.RefundNo,
		refund.IdempotencyKey, refund.AmountCents, refund.Currency,
		refund.Reason, refund.Status, refund.RequestedBy,
	)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	var persisted *paymentrecovery.RefundRecord
	err = tx.Ctx(ctx).Model("refunds").
		Where("provider", refund.Provider).
		Where("merchant_account", refund.Merchant).
		Where("idempotency_key", refund.IdempotencyKey).
		LockUpdate().
		Limit(1).
		Scan(&persisted)
	return persisted, affected > 0, err
}

func (r *PG) RefundByNoTxForUpdate(
	ctx context.Context,
	tx gdb.TX,
	refundNo string,
) (*paymentrecovery.RefundRecord, error) {
	var refund *paymentrecovery.RefundRecord
	err := tx.Ctx(ctx).Model("refunds").
		Where("refund_no", refundNo).
		LockUpdate().
		Limit(1).
		Scan(&refund)
	return refund, err
}

func (r *PG) RefundByNo(
	ctx context.Context,
	refundNo string,
) (*paymentrecovery.RefundRecord, error) {
	var refund *paymentrecovery.RefundRecord
	err := r.db.Model("refunds").Ctx(ctx).
		Where("refund_no", refundNo).
		Limit(1).
		Scan(&refund)
	return refund, err
}

func (r *PG) UpdateRefundTx(
	ctx context.Context,
	tx gdb.TX,
	refundID string,
	refund paymentrecovery.Refund,
) error {
	data := g.Map{
		"status":             refund.Status,
		"provider_refund_id": refund.ProviderRefundID,
		"revision":           refund.Revision,
		"last_observed_at":   refund.LastObservedAt,
		"updated_at":         gtime.Now(),
	}
	if refund.Status == paymentrecovery.RefundSucceeded {
		data["completed_at"] = refund.LastObservedAt
	}
	switch refund.Status {
	case paymentrecovery.RefundRequested,
		paymentrecovery.RefundSubmitting,
		paymentrecovery.RefundPending:
		data["next_reconcile_at"] = refund.LastObservedAt.UTC().Add(5 * time.Minute)
	default:
		data["next_reconcile_at"] = nil
	}
	_, err := tx.Ctx(ctx).Model("refunds").Where("id", refundID).Data(data).Update()
	return err
}

func (r *PG) MarkRefundSubmitting(ctx context.Context, refundNo string) error {
	_, err := r.db.Model("refunds").Ctx(ctx).
		Where("refund_no", refundNo).
		Where("status", paymentrecovery.RefundRequested).
		Data(g.Map{
			"status":            paymentrecovery.RefundSubmitting,
			"revision":          gdb.Raw("revision + 1"),
			"next_reconcile_at": gtime.Now(),
			"updated_at":        gtime.Now(),
		}).
		Update()
	return err
}

func (r *PG) RecordRefundReconciledTx(
	ctx context.Context,
	tx gdb.TX,
	refundID string,
	status paymentrecovery.RefundStatus,
	observedAt time.Time,
) error {
	data := g.Map{
		"last_reconciled_at":      observedAt.UTC(),
		"reconciliation_failures": 0,
		"reconciliation_error":    "",
		"updated_at":              gtime.Now(),
	}
	switch status {
	case paymentrecovery.RefundRequested,
		paymentrecovery.RefundSubmitting,
		paymentrecovery.RefundPending:
		data["next_reconcile_at"] = observedAt.UTC().Add(5 * time.Minute)
	default:
		data["next_reconcile_at"] = nil
	}
	_, err := tx.Ctx(ctx).Model("refunds").Where("id", refundID).Data(data).Update()
	return err
}

func (r *PG) DueRefunds(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]paymentrecovery.DueRefund, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var refunds []paymentrecovery.DueRefund
	err := r.db.Model("refunds r").Ctx(ctx).
		Fields("r.id, o.order_no, r.refund_no, r.provider, r.next_reconcile_at").
		InnerJoin("orders o", "o.id = r.order_id").
		WhereIn("r.status", []string{
			string(paymentrecovery.RefundSubmitting),
			string(paymentrecovery.RefundPending),
		}).
		WhereNotNull("r.next_reconcile_at").
		WhereLTE("r.next_reconcile_at", now.UTC()).
		OrderAsc("r.next_reconcile_at").
		Limit(limit).
		Scan(&refunds)
	return refunds, err
}

func (r *PG) DeferRefundReconciliation(
	ctx context.Context,
	refundID string,
	from, next time.Time,
) (bool, error) {
	result, err := r.db.Model("refunds").Ctx(ctx).
		Where("id", refundID).
		Where("next_reconcile_at", from.UTC()).
		Data(g.Map{
			"next_reconcile_at": next.UTC(),
			"updated_at":        gtime.Now(),
		}).
		Update()
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (r *PG) DisputeByProviderIDTxForUpdate(
	ctx context.Context,
	tx gdb.TX,
	provider, merchant, providerDisputeID string,
) (*paymentrecovery.DisputeRecord, error) {
	var dispute *paymentrecovery.DisputeRecord
	err := tx.Ctx(ctx).Model("disputes").
		Where("provider", provider).
		Where("merchant_account", merchant).
		Where("provider_dispute_id", providerDisputeID).
		LockUpdate().
		Limit(1).
		Scan(&dispute)
	return dispute, err
}

func (r *PG) CreateDisputeTx(
	ctx context.Context,
	tx gdb.TX,
	dispute *paymentrecovery.DisputeRecord,
) (*paymentrecovery.DisputeRecord, bool, error) {
	if dispute.ID == "" {
		dispute.ID = uuid.NewString()
	}
	result, err := tx.Ctx(ctx).Exec(`
INSERT INTO disputes (
    id, order_id, payment_attempt_id, provider, merchant_account,
    provider_dispute_id, provider_tx_id, status, provider_status,
    outcome_code, amount_cents, currency, reason_code, revision,
    opened_at, due_at, resolved_at, last_observed_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, merchant_account, provider_dispute_id) DO NOTHING`,
		dispute.ID, nullableString(dispute.OrderID),
		nullableString(dispute.PaymentAttemptID), dispute.Provider,
		dispute.Merchant, dispute.ProviderDisputeID, dispute.ProviderTxID,
		dispute.Status, dispute.ProviderStatus, dispute.OutcomeCode,
		dispute.AmountCents, dispute.Currency, dispute.ReasonCode,
		dispute.Revision, dispute.OpenedAt, dispute.DueAt,
		dispute.ResolvedAt, dispute.LastObservedAt,
	)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	persisted, err := r.DisputeByProviderIDTxForUpdate(
		ctx, tx, dispute.Provider, dispute.Merchant, dispute.ProviderDisputeID,
	)
	return persisted, affected > 0, err
}

func (r *PG) UpdateDisputeTx(
	ctx context.Context,
	tx gdb.TX,
	disputeID string,
	dispute paymentrecovery.Dispute,
) error {
	data := g.Map{
		"status":           dispute.Status,
		"provider_status":  dispute.ProviderStatus,
		"outcome_code":     dispute.OutcomeCode,
		"reason_code":      dispute.ReasonCode,
		"revision":         dispute.Revision,
		"opened_at":        nullableTime(dispute.OpenedAt),
		"due_at":           nullableTime(dispute.DueAt),
		"last_observed_at": dispute.LastObservedAt,
		"updated_at":       gtime.Now(),
	}
	if disputeTerminalStatus(dispute.Status) {
		data["resolved_at"] = dispute.LastObservedAt
	}
	_, err := tx.Ctx(ctx).Model("disputes").
		Where("id", disputeID).
		Data(data).
		Update()
	return err
}

func (r *PG) ApplyDisputeAccessPolicyTx(
	ctx context.Context,
	tx gdb.TX,
	orderID string,
	status paymentrecovery.DisputeStatus,
	reason string,
) error {
	switch status {
	case paymentrecovery.DisputeOpen,
		paymentrecovery.DisputeNeedsResponse,
		paymentrecovery.DisputeUnderReview:
		if _, err := tx.Ctx(ctx).Model("entitlements").
			Where("order_id", orderID).
			Where("state", "active").
			Data(g.Map{
				"state":            "suspended",
				"suspended_at":     gtime.Now(),
				"suspended_reason": strings.TrimSpace(reason),
			}).
			Update(); err != nil {
			return err
		}
		if _, err := tx.Ctx(ctx).Model("delivery_grants").
			Where("order_id", orderID).
			Where("state", "active").
			Data(g.Map{
				"state":            "suspended",
				"suspended_at":     gtime.Now(),
				"suspended_reason": strings.TrimSpace(reason),
			}).
			Update(); err != nil {
			return err
		}
		if _, err := r.QueueAssetGrantRevocationsByOrderIDTx(ctx, tx, orderID); err != nil {
			return err
		}
		_, err := tx.Ctx(ctx).Model("orders").Where("id", orderID).Data(g.Map{
			"dispute_state":  status,
			"delivery_state": model.DeliveryStateSuspended,
			"updated_at":     gtime.Now(),
		}).Update()
		return err
	case paymentrecovery.DisputeWon, paymentrecovery.DisputeClosed:
		if _, err := tx.Ctx(ctx).Model("entitlements").
			Where("order_id", orderID).
			Where("state", "suspended").
			Data(g.Map{
				"state":            "active",
				"suspended_at":     nil,
				"suspended_reason": "",
			}).
			Update(); err != nil {
			return err
		}
		if _, err := tx.Ctx(ctx).Model("delivery_grants").
			Where("order_id", orderID).
			Where("state", "suspended").
			Data(g.Map{
				"state":            "active",
				"suspended_at":     nil,
				"suspended_reason": "",
			}).
			Update(); err != nil {
			return err
		}
		_, err := tx.Ctx(ctx).Model("orders").Where("id", orderID).Data(g.Map{
			"dispute_state":  status,
			"delivery_state": model.DeliveryStateGranted,
			"updated_at":     gtime.Now(),
		}).Update()
		return err
	case paymentrecovery.DisputeLost, paymentrecovery.DisputeAccepted:
		if _, err := r.RevokeDeliveryGrantsByOrderIDTx(ctx, tx, orderID); err != nil {
			return err
		}
		if _, err := r.RevokeEntitlementsByOrderIDTx(
			ctx, tx, orderID, strings.TrimSpace(reason),
		); err != nil {
			return err
		}
		_, err := tx.Ctx(ctx).Model("orders").Where("id", orderID).Data(g.Map{
			"dispute_state":  status,
			"delivery_state": model.DeliveryStateRevoked,
			"updated_at":     gtime.Now(),
		}).Update()
		return err
	default:
		return paymentrecovery.ErrInvalidEvidence
	}
}

func (r *PG) RevokeEntitlementsByOrderIDTx(
	ctx context.Context,
	tx gdb.TX,
	orderID, reason string,
) (int64, error) {
	result, err := tx.Ctx(ctx).Model("entitlements").
		Where("order_id", orderID).
		WhereIn("state", []string{"active", "suspended"}).
		Data(g.Map{
			"state":            "revoked",
			"revoked_at":       gtime.Now(),
			"revoked_reason":   strings.TrimSpace(reason),
			"suspended_at":     nil,
			"suspended_reason": "",
		}).
		Update()
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *PG) ApplyRefundAmountTx(
	ctx context.Context,
	tx gdb.TX,
	orderID string,
	amountCents int,
	full bool,
) error {
	if full {
		_, err := tx.Ctx(ctx).Exec(`
UPDATE orders
SET refunded_amount_cents = refunded_amount_cents + ?,
    status = ?,
    delivery_state = ?,
    updated_at = now()
WHERE id = ?`, amountCents, model.OrderStatusRefunded, model.DeliveryStateRevoked, orderID)
		return err
	}
	_, err := tx.Ctx(ctx).Exec(`
UPDATE orders
SET refunded_amount_cents = refunded_amount_cents + ?,
    updated_at = now()
WHERE id = ?`, amountCents, orderID)
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

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(value time.Time) interface{} {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func disputeTerminalStatus(status paymentrecovery.DisputeStatus) bool {
	switch status {
	case paymentrecovery.DisputeWon,
		paymentrecovery.DisputeLost,
		paymentrecovery.DisputeAccepted,
		paymentrecovery.DisputeClosed:
		return true
	default:
		return false
	}
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
