// Package dao is the PostgreSQL data-access layer for the commerce service.
package dao

import (
	"context"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/google/uuid"

	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/paymentrecovery"
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
   SET state = 'revoked', revoked_at = now()
 WHERE order_id = ? AND state = 'active'`, orderID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (r *PG) MarkOrderRefundedTx(ctx context.Context, tx gdb.TX, orderID, providerRefundID string) error {
	data := g.Map{
		"status":         model.OrderStatusRefunded,
		"delivery_state": model.DeliveryStateRevoked,
		"updated_at":     gtime.Now(),
	}
	if providerRefundID != "" {
		data["provider_tx_id"] = providerRefundID
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

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
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
