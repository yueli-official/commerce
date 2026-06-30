// Package service implements the commerce domain logic:
// product lazy-create, order state machine, entitlement grant.
package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/model"
)

// OrderDesc carries the caller-supplied descriptor for a new order. SiteKey +
// ExternalID identify the product; PriceCents (paid) / PointsCost (points) is the
// buyer-confirmed price in the kind's unit.
type OrderDesc struct {
	SiteKey    string
	ExternalID string
	Kind       string
	Title      string
	Currency   string
	PriceCents int
	PointsCost int
}

type CheckoutDesc struct {
	BuyerSub   string
	BuyerEmail string
	Provider   string
	ReturnURL  string
	CancelURL  string
	Items      []CheckoutItemDesc
}

type CheckoutItemDesc struct {
	SiteKey      string
	ExternalID   string
	VariantID    string
	Title        string
	VariantTitle string
	SKU          string
	PriceCents   int
	PointsCost   int
	Currency     string
	DeliveryKind string
	DeliveryRef  string
	Quantity     int
}

type CheckoutGrantResult struct {
	Token       string
	State       string
	DeliveryRef string
}

type DeliveryResult struct {
	Grant             *model.DeliveryGrant
	Order             *model.Order
	Item              *model.OrderItem
	Token             string
	DownloadURL       string
	DownloadExpiresAt *time.Time
}

type PointsCheckoutResult struct {
	Order   *model.Order
	Grant   *CheckoutGrantResult
	Balance int64
}

type DeliveryDownloadResult struct {
	DeliveryRef string
	URL         string
	ExpiresAt   time.Time
}

type AssetDeliveryInput struct {
	AssetID   string
	SubjectID string
	ExpiresIn int
	Reason    string
}

type AssetDeliveryOutput struct {
	URL       string
	ExpiresAt time.Time
}

type AssetDeliveryClient interface {
	CreateDelivery(ctx context.Context, in AssetDeliveryInput) (AssetDeliveryOutput, error)
}

type OrderDetailResult struct {
	Order  *model.Order
	Items  []*model.OrderItem
	Events []*model.PaymentEvent
	Grants []*model.DeliveryGrant
}

// EntitledResult is the answer to an Entitled query.
type EntitledResult struct {
	Entitled bool
	Reason   string // "ok" | "not_purchased" | "insufficient_points" | "expired"
	Required struct {
		Kind       string
		PriceCents *int // paid: snapshot price; nil when product not yet created
		PointsCost *int // points: snapshot cost; nil otherwise
	}
}

// CheckinConfig is the daily check-in reward curve (config-driven). Points for a
// streak of N = Base + (N-1)*Step, capped at Cap; a missed day resets the streak.
type CheckinConfig struct {
	Base int
	Step int
	Cap  int
}

type DeliveryConfig struct {
	SigningSecret string
	PublicBaseURL string
	TTL           time.Duration
}

type Option func(*Service)

func WithDeliveryConfig(cfg DeliveryConfig) Option {
	return func(s *Service) {
		s.ConfigureDelivery(cfg)
	}
}

func WithDeliveryMailer(mailer DeliveryMailer) Option {
	return func(s *Service) {
		s.ConfigureDeliveryMailer(mailer)
	}
}

func WithAssetDeliveryClient(client AssetDeliveryClient) Option {
	return func(s *Service) {
		s.ConfigureAssetDeliveryClient(client)
	}
}

// Service holds the DAO and implements the commerce domain logic.
type Service struct {
	db            *dao.PG
	checkin       CheckinConfig
	delivery      DeliveryConfig
	mailer        DeliveryMailer
	assetDelivery AssetDeliveryClient
}

// New constructs a Service.
func New(db *dao.PG, checkin CheckinConfig, opts ...Option) *Service {
	s := &Service{db: db, checkin: checkin, delivery: DeliveryConfig{TTL: 15 * time.Minute}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) ConfigureDelivery(cfg DeliveryConfig) {
	if cfg.TTL <= 0 {
		cfg.TTL = 15 * time.Minute
	}
	cfg.SigningSecret = strings.TrimSpace(cfg.SigningSecret)
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	s.delivery = cfg
}

func (s *Service) ConfigureDeliveryMailer(mailer DeliveryMailer) {
	s.mailer = mailer
}

func (s *Service) ConfigureAssetDeliveryClient(client AssetDeliveryClient) {
	s.assetDelivery = client
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

func (s *Service) CreateCheckout(ctx context.Context, desc CheckoutDesc) (*model.Order, error) {
	buyer, err := s.resolveBuyer(ctx, desc)
	if err != nil {
		return nil, err
	}
	if len(desc.Items) == 0 {
		return nil, commerceerr.NotifyInvalid("checkout requires at least one item")
	}
	if desc.Provider == "" {
		desc.Provider = "alipay"
	}

	var (
		total          int
		items          []*model.OrderItem
		firstProductID string
		currency       = "CNY"
	)
	for _, in := range desc.Items {
		if in.Quantity <= 0 {
			in.Quantity = 1
		}
		if in.Currency != "" {
			currency = in.Currency
		}
		if in.PriceCents < 0 || in.PointsCost < 0 {
			return nil, commerceerr.NotifyInvalid("checkout item price cannot be negative")
		}
		lineTotal := in.PriceCents * in.Quantity
		total += lineTotal
		p := &model.Product{
			SiteKey: in.SiteKey, ExternalID: in.ExternalID, Kind: model.ProductKindPaid,
			Title: in.Title, PriceCents: in.PriceCents, PointsCost: in.PointsCost, Currency: currency, Status: model.ProductStatusActive,
		}
		if err := s.db.UpsertProduct(ctx, p); err != nil {
			return nil, err
		}
		if firstProductID == "" {
			firstProductID = p.ID
		}
		items = append(items, &model.OrderItem{
			SiteKey: in.SiteKey, ExternalID: in.ExternalID, ProductID: p.ID, VariantID: in.VariantID,
			TitleSnapshot: in.Title, VariantTitleSnapshot: in.VariantTitle, SKUSnapshot: in.SKU,
			Quantity: in.Quantity, UnitPriceCents: in.PriceCents, UnitPointsCost: in.PointsCost,
			Currency: currency, DeliveryKindSnapshot: defaultString(in.DeliveryKind, "asset_file"), DeliveryRefSnapshot: in.DeliveryRef,
		})
	}
	if total <= 0 {
		return nil, commerceerr.NotifyInvalid("checkout amount must be positive")
	}
	o := &model.Order{
		OrderNo: NewOrderNo(), Sub: buyer.BuyerSub, ProductID: firstProductID, AmountCents: total, Currency: currency,
		Status: model.OrderStatusPaying, Gateway: desc.Provider, BuyerID: buyer.ID, BuyerSub: buyer.BuyerSub, BuyerEmail: buyer.BuyerEmail,
		PaymentProvider: desc.Provider, ReturnURL: desc.ReturnURL, CancelURL: desc.CancelURL, DeliveryState: model.DeliveryStatePending,
	}
	if err := s.db.InsertCheckoutOrder(ctx, o, items); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Service) RedeemCheckout(ctx context.Context, desc CheckoutDesc) (*PointsCheckoutResult, error) {
	if strings.TrimSpace(desc.BuyerSub) == "" {
		return nil, commerceerr.Forbidden()
	}
	desc.Provider = model.ProductKindPoints
	buyer, err := s.resolveBuyer(ctx, desc)
	if err != nil {
		return nil, err
	}
	if len(desc.Items) == 0 {
		return nil, commerceerr.NotifyInvalid("checkout requires at least one item")
	}

	var (
		totalPoints    int
		items          []*model.OrderItem
		firstProductID string
	)
	for _, in := range desc.Items {
		if in.Quantity <= 0 {
			in.Quantity = 1
		}
		if in.PointsCost < 1 {
			return nil, commerceerr.InvalidRequest("pointsCost is required for a points checkout")
		}
		if in.PriceCents < 0 {
			return nil, commerceerr.NotifyInvalid("checkout item price cannot be negative")
		}
		totalPoints += in.PointsCost * in.Quantity
		p := &model.Product{
			SiteKey: in.SiteKey, ExternalID: in.ExternalID, Kind: model.ProductKindPoints,
			Title: in.Title, PriceCents: in.PriceCents, PointsCost: in.PointsCost, Currency: defaultString(in.Currency, "POINTS"),
			Status: model.ProductStatusActive,
		}
		if err := s.db.UpsertProduct(ctx, p); err != nil {
			return nil, err
		}
		if firstProductID == "" {
			firstProductID = p.ID
		}
		items = append(items, &model.OrderItem{
			SiteKey: in.SiteKey, ExternalID: in.ExternalID, ProductID: p.ID, VariantID: in.VariantID,
			TitleSnapshot: in.Title, VariantTitleSnapshot: in.VariantTitle, SKUSnapshot: in.SKU,
			Quantity: in.Quantity, UnitPriceCents: in.PriceCents, UnitPointsCost: in.PointsCost,
			Currency: defaultString(in.Currency, "POINTS"), DeliveryKindSnapshot: defaultString(in.DeliveryKind, "asset_file"),
			DeliveryRefSnapshot: in.DeliveryRef,
		})
	}
	if totalPoints <= 0 {
		return nil, commerceerr.InvalidRequest("pointsCost is required for a points checkout")
	}

	rawToken, tokenHash, err := newDeliveryToken()
	if err != nil {
		return nil, err
	}
	o := &model.Order{
		OrderNo: NewOrderNo(), Sub: buyer.BuyerSub, ProductID: firstProductID, AmountCents: 0, Currency: "POINTS",
		Status: model.OrderStatusPaying, Gateway: model.ProductKindPoints, BuyerID: buyer.ID, BuyerSub: buyer.BuyerSub, BuyerEmail: buyer.BuyerEmail,
		PaymentProvider: model.ProductKindPoints, DeliveryState: model.DeliveryStatePending,
	}
	now := gtime.New(time.Now())
	first := items[0]
	spendRef := ""
	err = s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if err := s.db.InsertCheckoutOrderTx(ctx, tx, o, items); err != nil {
			return err
		}
		pointsToSpend := 0
		for _, item := range items {
			inserted, err := s.db.GrantEntitlementTx(ctx, tx, &model.Entitlement{
				Sub: itemBuyerSub(o), ProductID: item.ProductID, Source: model.EntitlementSourcePoints, OrderID: &o.ID,
			})
			if err != nil {
				return err
			}
			if inserted {
				pointsToSpend += item.UnitPointsCost * item.Quantity
			}
		}
		if pointsToSpend > 0 {
			spendRef = o.ID
			ok, err := s.db.SpendCreditsTx(ctx, tx, buyer.BuyerSub, pointsToSpend, model.CreditsSourceRedeem, spendRef)
			if err != nil {
				return err
			}
			if !ok {
				return commerceerr.InsufficientPoints(pointsToSpend)
			}
		}
		if err := s.db.FulfillCheckoutTx(ctx, tx, o.ID, "POINTS-"+o.OrderNo, now); err != nil {
			return err
		}
		if err := s.db.InsertPaymentEventTx(ctx, tx, &model.PaymentEvent{
			OrderID: o.ID, Provider: model.ProductKindPoints, EventType: "redeem", ProviderEventID: spendRef,
			AmountCents: 0, Success: true, Message: "points checkout redeemed",
		}); err != nil {
			return err
		}
		return s.db.InsertDeliveryGrantTx(ctx, tx, &model.DeliveryGrant{
			OrderID: o.ID, OrderItemID: first.ID, BuyerSub: o.BuyerSub, BuyerEmail: o.BuyerEmail,
			TokenHash: tokenHash, DeliveryRef: first.DeliveryRefSnapshot, State: "active",
		})
	})
	if err != nil {
		return nil, err
	}
	bal, err := s.db.GetBalance(ctx, buyer.BuyerSub)
	if err != nil {
		return nil, err
	}
	s.sendDeliveryMail(ctx, o, first, rawToken)
	return &PointsCheckoutResult{
		Order:   o,
		Grant:   &CheckoutGrantResult{Token: rawToken, State: model.DeliveryStateGranted, DeliveryRef: first.DeliveryRefSnapshot},
		Balance: bal,
	}, nil
}

func (s *Service) SettleCheckout(ctx context.Context, orderNo, provider, providerTxID string, amountCents int) (*CheckoutGrantResult, error) {
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, commerceerr.OrderNotFound(orderNo)
	}
	if o.Status == model.OrderStatusFulfilled {
		return &CheckoutGrantResult{State: model.DeliveryStateGranted}, nil
	}
	if o.Status != model.OrderStatusPaying {
		return nil, commerceerr.OrderInvalidState(o.Status, model.OrderStatusFulfilled)
	}
	if amountCents != o.AmountCents {
		return nil, commerceerr.NotifyInvalid("amount mismatch")
	}
	items, err := s.db.OrderItems(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, commerceerr.NotifyInvalid("checkout has no items")
	}

	rawToken, tokenHash, err := newDeliveryToken()
	if err != nil {
		return nil, err
	}
	now := gtime.New(time.Now())
	first := items[0]
	err = s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if err := s.db.FulfillCheckoutTx(ctx, tx, o.ID, providerTxID, now); err != nil {
			return err
		}
		if err := s.db.InsertPaymentEventTx(ctx, tx, &model.PaymentEvent{
			OrderID: o.ID, Provider: defaultString(provider, o.PaymentProvider), EventType: "settle",
			ProviderEventID: providerTxID, AmountCents: amountCents, Success: true, Message: "checkout settled",
		}); err != nil {
			return err
		}
		if o.BuyerSub != "" {
			for _, item := range items {
				if err := s.db.InsertEntitlementTx(ctx, tx, &model.Entitlement{
					Sub: itemBuyerSub(o), ProductID: item.ProductID, Source: model.EntitlementSourceOrder, OrderID: &o.ID,
				}); err != nil {
					return err
				}
			}
		}
		return s.db.InsertDeliveryGrantTx(ctx, tx, &model.DeliveryGrant{
			OrderID: o.ID, OrderItemID: first.ID, BuyerSub: o.BuyerSub, BuyerEmail: o.BuyerEmail,
			TokenHash: tokenHash, DeliveryRef: first.DeliveryRefSnapshot, State: "active",
		})
	})
	if err != nil {
		return nil, err
	}
	s.sendDeliveryMail(ctx, o, first, rawToken)
	return &CheckoutGrantResult{Token: rawToken, State: model.DeliveryStateGranted, DeliveryRef: first.DeliveryRefSnapshot}, nil
}

func (s *Service) SetCheckoutPaymentSession(ctx context.Context, orderNo, sessionID string) error {
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	if o == nil {
		return commerceerr.OrderNotFound(orderNo)
	}
	if o.Status != model.OrderStatusPaying {
		return commerceerr.OrderInvalidState(o.Status, model.OrderStatusPaying)
	}
	return s.db.UpdatePaymentSession(ctx, o.ID, sessionID)
}

func (s *Service) resolveBuyer(ctx context.Context, desc CheckoutDesc) (*model.Buyer, error) {
	email := normalizeEmail(desc.BuyerEmail)
	if strings.TrimSpace(desc.BuyerSub) == "" && email == "" {
		return nil, commerceerr.NotifyInvalid("checkout requires buyer email or user")
	}
	b := &model.Buyer{
		BuyerSub: strings.TrimSpace(desc.BuyerSub), BuyerEmail: strings.TrimSpace(desc.BuyerEmail), EmailNormalized: email,
	}
	if b.BuyerSub != "" {
		b.Kind = model.BuyerKindUser
	} else {
		b.Kind = model.BuyerKindGuest
		b.BuyerEmail = email
	}
	if err := s.db.UpsertBuyer(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
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

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func itemBuyerSub(o *model.Order) string {
	if o.BuyerSub != "" {
		return o.BuyerSub
	}
	return o.Sub
}

func newDeliveryToken() (string, string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(b[:])
	return raw, hashDeliveryToken(raw), nil
}

func hashDeliveryToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
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

// CancelOrder transitions an order from "pending" or "paying" to "cancelled".
// It is intentionally lenient: if the order is already in a terminal state
// (paid, cancelled) or does not exist, it returns nil (safe to call best-effort).
func (s *Service) CancelOrder(ctx context.Context, orderNo string) error {
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	if o == nil {
		// Order not found — nothing to cancel.
		return nil
	}
	if o.Status != model.OrderStatusPending && o.Status != model.OrderStatusPaying {
		// Already in a terminal state; no-op.
		return nil
	}
	return s.db.UpdateOrderStatus(ctx, o.ID, model.OrderStatusCancelled)
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

func (s *Service) ListOrders(ctx context.Context, status, q string, limit, offset int) ([]*model.Order, error) {
	return s.db.ListOrders(ctx, strings.TrimSpace(status), strings.TrimSpace(q), limit, offset)
}

func (s *Service) OrderItems(ctx context.Context, orderID string) ([]*model.OrderItem, error) {
	return s.db.OrderItems(ctx, orderID)
}

func (s *Service) OrderDetail(ctx context.Context, orderNo string) (*OrderDetailResult, error) {
	order, err := s.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	items, err := s.db.OrderItems(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	events, err := s.db.PaymentEventsByOrderID(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	grants, err := s.db.DeliveryGrantsByOrderID(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	return &OrderDetailResult{Order: order, Items: items, Events: events, Grants: grants}, nil
}

func (s *Service) ResendDelivery(ctx context.Context, orderNo string) (*CheckoutGrantResult, error) {
	return s.createManualDeliveryGrant(ctx, orderNo, true)
}

func (s *Service) GrantDelivery(ctx context.Context, orderNo string) (*CheckoutGrantResult, error) {
	return s.createManualDeliveryGrant(ctx, orderNo, false)
}

func (s *Service) RevokeDelivery(ctx context.Context, orderNo string) (int64, error) {
	order, err := s.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return 0, err
	}
	var revoked int64
	err = s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		n, err := s.db.RevokeDeliveryGrantsByOrderIDTx(ctx, tx, order.ID)
		if err != nil {
			return err
		}
		revoked = n
		return s.db.InsertPaymentEventTx(ctx, tx, &model.PaymentEvent{
			OrderID: order.ID, Provider: defaultString(order.PaymentProvider, "admin"), EventType: "delivery_revoke",
			ProviderEventID: "", AmountCents: order.AmountCents, Success: true, Message: "delivery grants revoked by admin",
		})
	})
	return revoked, err
}

func (s *Service) MarkRefunded(ctx context.Context, orderNo, providerRefundID string) error {
	order, err := s.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	return s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := s.db.RevokeDeliveryGrantsByOrderIDTx(ctx, tx, order.ID); err != nil {
			return err
		}
		if err := s.db.MarkOrderRefundedTx(ctx, tx, order.ID, providerRefundID); err != nil {
			return err
		}
		return s.db.InsertPaymentEventTx(ctx, tx, &model.PaymentEvent{
			OrderID: order.ID, Provider: defaultString(order.PaymentProvider, "admin"), EventType: "refund",
			ProviderEventID: providerRefundID, AmountCents: order.AmountCents, Success: true, Message: "order refunded",
		})
	})
}

func (s *Service) createManualDeliveryGrant(ctx context.Context, orderNo string, sendMail bool) (*CheckoutGrantResult, error) {
	order, err := s.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if order.Status != model.OrderStatusFulfilled && order.Status != model.OrderStatusPaid {
		return nil, commerceerr.OrderInvalidState(order.Status, model.OrderStatusFulfilled)
	}
	items, err := s.db.OrderItems(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, commerceerr.NotifyInvalid("order has no items")
	}
	rawToken, tokenHash, err := newDeliveryToken()
	if err != nil {
		return nil, err
	}
	first := items[0]
	err = s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if err := s.db.InsertDeliveryGrantTx(ctx, tx, &model.DeliveryGrant{
			OrderID: order.ID, OrderItemID: first.ID, BuyerSub: order.BuyerSub, BuyerEmail: order.BuyerEmail,
			TokenHash: tokenHash, DeliveryRef: first.DeliveryRefSnapshot, State: "active",
		}); err != nil {
			return err
		}
		return s.db.InsertPaymentEventTx(ctx, tx, &model.PaymentEvent{
			OrderID: order.ID, Provider: "admin", EventType: "delivery_grant",
			ProviderEventID: "", AmountCents: order.AmountCents, Success: true, Message: "delivery grant created by admin",
		})
	})
	if err != nil {
		return nil, err
	}
	if sendMail {
		s.sendDeliveryMail(ctx, order, first, rawToken)
	}
	return &CheckoutGrantResult{Token: rawToken, State: model.DeliveryStateGranted, DeliveryRef: first.DeliveryRefSnapshot}, nil
}

func (s *Service) DeliveryByToken(ctx context.Context, token string) (*DeliveryResult, error) {
	token = strings.TrimSpace(token)
	grant, err := s.db.DeliveryGrantByTokenHash(ctx, hashDeliveryToken(token))
	if err != nil {
		return nil, err
	}
	if grant == nil {
		return nil, commerceerr.OrderNotFound("delivery")
	}
	res, err := s.deliveryResult(ctx, grant)
	if err != nil {
		return nil, err
	}
	res.Token = token
	s.attachDownloadURL(res)
	return res, nil
}

func (s *Service) Purchases(ctx context.Context, sub string, limit, offset int) ([]*DeliveryResult, error) {
	grants, err := s.db.DeliveryGrantsByBuyerSub(ctx, strings.TrimSpace(sub), limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]*DeliveryResult, 0, len(grants))
	for _, grant := range grants {
		res, err := s.deliveryResult(ctx, grant)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

func (s *Service) deliveryResult(ctx context.Context, grant *model.DeliveryGrant) (*DeliveryResult, error) {
	order, err := s.db.GetOrderByID(ctx, grant.OrderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, commerceerr.OrderNotFound(grant.OrderID)
	}
	var item *model.OrderItem
	if grant.OrderItemID != "" {
		item, err = s.db.OrderItemByID(ctx, grant.OrderItemID)
		if err != nil {
			return nil, err
		}
	}
	return &DeliveryResult{Grant: grant, Order: order, Item: item}, nil
}

func (s *Service) ResolveDeliveryDownload(ctx context.Context, token, exp, sig string) (DeliveryDownloadResult, error) {
	if strings.TrimSpace(s.delivery.SigningSecret) == "" {
		return DeliveryDownloadResult{}, commerceerr.InvalidRequest("delivery signing is not configured")
	}
	expiresUnix, err := strconv.ParseInt(strings.TrimSpace(exp), 10, 64)
	if err != nil {
		return DeliveryDownloadResult{}, commerceerr.InvalidRequest("invalid delivery expiry")
	}
	expiresAt := time.Unix(expiresUnix, 0).UTC()
	if !time.Now().UTC().Before(expiresAt) {
		return DeliveryDownloadResult{}, commerceerr.InvalidRequest("delivery link expired")
	}
	delivery, err := s.DeliveryByToken(ctx, token)
	if err != nil {
		return DeliveryDownloadResult{}, err
	}
	expected := s.deliverySignature(token, exp, delivery.Grant.DeliveryRef)
	if !hmac.Equal([]byte(expected), []byte(strings.TrimSpace(sig))) {
		return DeliveryDownloadResult{}, commerceerr.InvalidRequest("invalid delivery signature")
	}
	res := DeliveryDownloadResult{DeliveryRef: delivery.Grant.DeliveryRef, ExpiresAt: expiresAt}
	if s.assetDelivery == nil || strings.TrimSpace(delivery.Grant.DeliveryRef) == "" {
		return res, nil
	}
	assetOut, err := s.assetDelivery.CreateDelivery(ctx, AssetDeliveryInput{
		AssetID:   delivery.Grant.DeliveryRef,
		SubjectID: deliverySubject(delivery.Order),
		ExpiresIn: secondsUntil(expiresAt),
		Reason:    "commerce:" + delivery.Order.OrderNo,
	})
	if err != nil {
		return DeliveryDownloadResult{}, err
	}
	res.URL = assetOut.URL
	if !assetOut.ExpiresAt.IsZero() && assetOut.ExpiresAt.Before(res.ExpiresAt) {
		res.ExpiresAt = assetOut.ExpiresAt
	}
	return res, nil
}

func deliverySubject(order *model.Order) string {
	if order == nil {
		return ""
	}
	if order.BuyerSub != "" {
		return order.BuyerSub
	}
	return order.BuyerEmail
}

func secondsUntil(t time.Time) int {
	seconds := int(time.Until(t).Seconds())
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (s *Service) attachDownloadURL(res *DeliveryResult) {
	if res == nil || res.Grant == nil || strings.TrimSpace(res.Token) == "" || strings.TrimSpace(s.delivery.SigningSecret) == "" {
		return
	}
	if res.Item != nil && res.Item.DeliveryKindSnapshot != "" && res.Item.DeliveryKindSnapshot != "asset_file" {
		return
	}
	url, expiresAt := s.signedDeliveryURL(res.Token, res.Grant.DeliveryRef)
	res.DownloadURL = url
	res.DownloadExpiresAt = &expiresAt
}

func (s *Service) signedDeliveryURL(token, deliveryRef string) (string, time.Time) {
	expiresAt := time.Now().UTC().Add(s.delivery.TTL)
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	sig := s.deliverySignature(token, exp, deliveryRef)
	q := url.Values{}
	q.Set("exp", exp)
	q.Set("sig", sig)
	path := "/api/v1/delivery/" + url.PathEscape(token) + "/download"
	return s.delivery.PublicBaseURL + path + "?" + q.Encode(), expiresAt
}

func (s *Service) deliverySignature(token, exp, deliveryRef string) string {
	mac := hmac.New(sha256.New, []byte(s.delivery.SigningSecret))
	_, _ = fmt.Fprintf(mac, "%s\n%s\n%s", strings.TrimSpace(token), strings.TrimSpace(exp), strings.TrimSpace(deliveryRef))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) sendDeliveryMail(ctx context.Context, order *model.Order, item *model.OrderItem, rawToken string) {
	if s.mailer == nil || order == nil || item == nil || strings.TrimSpace(order.BuyerEmail) == "" {
		return
	}
	url := ""
	if strings.TrimSpace(s.delivery.SigningSecret) != "" {
		url, _ = s.signedDeliveryURL(rawToken, item.DeliveryRefSnapshot)
	}
	if err := s.mailer.SendDelivery(ctx, DeliveryMail{
		To:          order.BuyerEmail,
		OrderNo:     order.OrderNo,
		Title:       item.TitleSnapshot,
		DeliveryRef: item.DeliveryRefSnapshot,
		DeliveryURL: url,
	}); err != nil {
		g.Log().Warningf(ctx, "delivery mail failed order=%s to=%s: %v", order.OrderNo, order.BuyerEmail, err)
	}
}

// Entitled reports whether sub is entitled to the product identified by (siteKey, externalID).
// Reason is "ok" when entitled, otherwise "not_purchased" (no entitlement, or the product
// has never been created).
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

	// Fill in the product snapshot for the caller (price in the kind's unit).
	result.Required.Kind = p.Kind
	if p.Kind == model.ProductKindPoints {
		pts := p.PointsCost
		result.Required.PointsCost = &pts
	} else {
		pc := p.PriceCents
		result.Required.PriceCents = &pc
	}

	exists, err := s.db.EntitlementExists(ctx, sub, p.ID)
	if err != nil {
		return result, err
	}
	if exists {
		result.Entitled = true
		result.Reason = "ok"
		return result, nil
	}
	result.Entitled = false
	result.Reason = "not_purchased"
	// For a points product the user doesn't own, distinguish "can afford / can't"
	// so the button can read "兑换" vs "积分不足".
	if p.Kind == model.ProductKindPoints {
		bal, err := s.db.GetBalance(ctx, sub)
		if err != nil {
			return result, err
		}
		if bal < int64(p.PointsCost) {
			result.Reason = "insufficient_points"
		}
	}
	return result, nil
}

// ─── M2: check-in + credits + points redemption ─────────────────────────────

// checkinPoints is the reward for a streak of n consecutive days.
func (s *Service) checkinPoints(streak int) int {
	p := s.checkin.Base + (streak-1)*s.checkin.Step
	if s.checkin.Cap > 0 && p > s.checkin.Cap {
		p = s.checkin.Cap
	}
	if p < 0 {
		p = 0
	}
	return p
}

// CheckinResult is the outcome of a daily check-in.
type CheckinResult struct {
	Date             string
	Streak           int
	PointsAwarded    int
	Balance          int64
	AlreadyCheckedIn bool
}

// Checkin records today's check-in for sub and credits the streak reward.
// Idempotent per UTC day: a second call the same day earns nothing and reports
// AlreadyCheckedIn. The streak continues yesterday's (or resets to 1).
func (s *Service) Checkin(ctx context.Context, sub string) (CheckinResult, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Fast path: already checked in today (no write).
	existing, err := s.db.GetCheckin(ctx, sub, today)
	if err != nil {
		return CheckinResult{}, err
	}
	if existing != nil {
		bal, err := s.db.GetBalance(ctx, sub)
		if err != nil {
			return CheckinResult{}, err
		}
		return CheckinResult{Date: today, Streak: existing.Streak, PointsAwarded: existing.PointsAwarded, Balance: bal, AlreadyCheckedIn: true}, nil
	}

	streak := 1
	y, err := s.db.GetCheckin(ctx, sub, yesterday)
	if err != nil {
		return CheckinResult{}, err
	}
	if y != nil {
		streak = y.Streak + 1
	}
	points := s.checkinPoints(streak)

	already := false
	if err := s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		inserted, err := s.db.InsertCheckinTx(ctx, tx, &model.CheckinRecord{
			Sub: sub, CheckinDate: today, Streak: streak, PointsAwarded: points,
		})
		if err != nil {
			return err
		}
		if !inserted {
			already = true // a concurrent check-in won the race; don't double-earn
			return nil
		}
		return s.db.EarnCreditsTx(ctx, tx, sub, points, model.CreditsSourceCheckin, today)
	}); err != nil {
		return CheckinResult{}, err
	}

	bal, err := s.db.GetBalance(ctx, sub)
	if err != nil {
		return CheckinResult{}, err
	}
	if already {
		rec, err := s.db.GetCheckin(ctx, sub, today)
		if err != nil {
			return CheckinResult{}, err
		}
		res := CheckinResult{Date: today, Balance: bal, AlreadyCheckedIn: true}
		if rec != nil {
			res.Streak, res.PointsAwarded = rec.Streak, rec.PointsAwarded
		}
		return res, nil
	}
	return CheckinResult{Date: today, Streak: streak, PointsAwarded: points, Balance: bal}, nil
}

// Balance returns the subscriber's current points balance.
func (s *Service) Balance(ctx context.Context, sub string) (int64, error) {
	return s.db.GetBalance(ctx, sub)
}

// Ledger returns a page of the subscriber's points ledger plus the total count.
func (s *Service) Ledger(ctx context.Context, sub string, limit, offset int) ([]model.LedgerEntry, int, error) {
	return s.db.ListLedger(ctx, sub, limit, offset)
}

// CheckinStatus is today's check-in state for a subscriber (read-only).
type CheckinStatus struct {
	CheckedInToday bool
	Streak         int
	Balance        int64
}

// CheckinStatus reports whether sub has checked in today (+ current streak +
// balance) without mutating anything — drives the check-in button.
func (s *Service) CheckinStatus(ctx context.Context, sub string) (CheckinStatus, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	rec, err := s.db.GetCheckin(ctx, sub, today)
	if err != nil {
		return CheckinStatus{}, err
	}
	bal, err := s.db.GetBalance(ctx, sub)
	if err != nil {
		return CheckinStatus{}, err
	}
	st := CheckinStatus{Balance: bal}
	if rec != nil {
		st.CheckedInToday = true
		st.Streak = rec.Streak
	}
	return st, nil
}

// RedeemResult is the outcome of a points redemption.
type RedeemResult struct {
	Entitled bool
	Balance  int64
}

// Redeem spends points to grant sub an entitlement to the product in desc.
// Idempotent: a repeat redeem on an already-owned product grants and spends
// nothing (the fresh-grant check gates the spend, so points are never
// double-charged). Insufficient balance → commerce.insufficient_points.
func (s *Service) Redeem(ctx context.Context, sub string, desc OrderDesc) (RedeemResult, error) {
	p := &model.Product{
		SiteKey: desc.SiteKey, ExternalID: desc.ExternalID, Kind: model.ProductKindPoints,
		Title: desc.Title, PointsCost: desc.PointsCost, Currency: desc.Currency, Status: model.ProductStatusActive,
	}
	if err := s.db.UpsertProduct(ctx, p); err != nil {
		return RedeemResult{}, err
	}
	if err := s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		inserted, err := s.db.GrantEntitlementTx(ctx, tx, &model.Entitlement{
			Sub: sub, ProductID: p.ID, Source: model.EntitlementSourcePoints,
		})
		if err != nil {
			return err
		}
		if !inserted {
			return nil // already owned: grant + spend are no-ops
		}
		ok, err := s.db.SpendCreditsTx(ctx, tx, sub, desc.PointsCost, model.CreditsSourceRedeem, p.ID)
		if err != nil {
			return err
		}
		if !ok {
			return commerceerr.InsufficientPoints(desc.PointsCost)
		}
		return nil
	}); err != nil {
		return RedeemResult{}, err
	}
	bal, err := s.db.GetBalance(ctx, sub)
	if err != nil {
		return RedeemResult{}, err
	}
	return RedeemResult{Entitled: true, Balance: bal}, nil
}
