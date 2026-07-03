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
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
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
	SiteKey               string
	ExternalID            string
	VariantID             string
	Title                 string
	VariantTitle          string
	SKU                   string
	PriceCents            int
	PointsCost            int
	Currency              string
	DeliveryKind          string
	DeliveryRef           string
	PurchaseLimitPerBuyer int
	Quantity              int
}

type CurrentCheckoutItemInput struct {
	SiteKey    string
	ExternalID string
	VariantID  string
}

type CurrentCheckoutItemResult struct {
	SiteKey               string
	ExternalID            string
	VariantID             string
	Title                 string
	VariantTitle          string
	SKU                   string
	PriceCents            int
	PointsCost            int
	Currency              string
	DeliveryKind          string
	DeliveryRef           string
	PurchaseLimitPerBuyer int
}

type CurrentCheckoutItemResolver interface {
	CurrentCheckoutItem(ctx context.Context, in CurrentCheckoutItemInput) (CurrentCheckoutItemResult, error)
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

type PurchaseFilter struct {
	Sub   string
	Q     string
	State string
}

type PointsCheckoutResult struct {
	Order   *model.Order
	Grant   *CheckoutGrantResult
	Balance int64
}

type FreeCheckoutResult struct {
	Order *model.Order
	Grant *CheckoutGrantResult
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

type CurrentDeliveryInput struct {
	SiteKey    string
	ExternalID string
	VariantID  string
}

type CurrentDeliveryResult struct {
	DeliveryKind string
	DeliveryRef  string
}

type CurrentDeliveryResolver interface {
	CurrentDelivery(ctx context.Context, in CurrentDeliveryInput) (CurrentDeliveryResult, error)
}

type OrderDetailResult struct {
	Order  *model.Order
	Items  []*model.OrderItem
	Events []*model.PaymentEvent
	Grants []*model.DeliveryGrant
}

type CheckoutStatusResult struct {
	Order *model.Order
	Grant *model.DeliveryGrant
}

type PaymentMethodInput struct {
	Provider  string
	Label     string
	Enabled   bool
	SortOrder int
}

type PaymentMethodConfig struct {
	Provider    string
	Label       string
	Method      string
	Enabled     bool
	SortOrder   int
	Description string
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

type deliveryAccessRulesResult struct {
	ExpiresAt    *time.Time
	MaxDownloads int
	DownloadTTL  time.Duration
}

const reusableCheckoutWindow = 10 * time.Minute

var paymentMethodDefinitions = map[string]PaymentMethodConfig{
	"alipay": {
		Provider: "alipay", Label: "Alipay", Method: "redirect", SortOrder: 10,
		Description: "Page redirect checkout",
	},
	"wechat": {
		Provider: "wechat", Label: "WeChat Pay", Method: "native_qr", SortOrder: 20,
		Description: "Native QR checkout",
	},
	"paypal": {
		Provider: "paypal", Label: "PayPal", Method: "browser_button", SortOrder: 30,
		Description: "Browser approval and server capture",
	},
}

var paymentMethodOrder = []string{"alipay", "wechat", "paypal"}

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

func WithCurrentDeliveryResolver(resolver CurrentDeliveryResolver) Option {
	return func(s *Service) {
		s.ConfigureCurrentDeliveryResolver(resolver)
	}
}

func WithCurrentCheckoutItemResolver(resolver CurrentCheckoutItemResolver) Option {
	return func(s *Service) {
		s.ConfigureCurrentCheckoutItemResolver(resolver)
	}
}

// Service holds the DAO and implements the commerce domain logic.
type Service struct {
	db              *dao.PG
	checkin         CheckinConfig
	delivery        DeliveryConfig
	mailer          DeliveryMailer
	assetDelivery   AssetDeliveryClient
	currentDelivery CurrentDeliveryResolver
	currentCheckout CurrentCheckoutItemResolver
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

func (s *Service) ConfigureCurrentDeliveryResolver(resolver CurrentDeliveryResolver) {
	s.currentDelivery = resolver
}

func (s *Service) ConfigureCurrentCheckoutItemResolver(resolver CurrentCheckoutItemResolver) {
	s.currentCheckout = resolver
}

// CreateOrder lazily creates a legacy product, then inserts a new order in
// "pending" status. If the product already exists, its stored catalog snapshot
// is authoritative; callers cannot overwrite price or title through this path.
func (s *Service) CreateOrder(ctx context.Context, sub string, desc OrderDesc) (*model.Order, *model.Product, error) {
	p, err := s.db.GetProductByExternal(ctx, desc.SiteKey, desc.ExternalID)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		p = &model.Product{
			SiteKey:    desc.SiteKey,
			ExternalID: desc.ExternalID,
			Kind:       desc.Kind,
			Title:      desc.Title,
			PriceCents: desc.PriceCents,
			PointsCost: desc.PointsCost,
			Currency:   desc.Currency,
			Status:     model.ProductStatusActive,
		}
		if err := s.db.UpsertProduct(ctx, p); err != nil {
			return nil, nil, err
		}
	}
	if p.Status != "" && p.Status != model.ProductStatusActive {
		return nil, nil, commerceerr.InvalidRequest("product is not active")
	}

	// Build and insert the order.
	o := &model.Order{
		OrderNo:     NewOrderNo(),
		Sub:         sub,
		ProductID:   p.ID,
		AmountCents: p.PriceCents,
		Currency:    p.Currency,
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
	if err := validateCheckoutItems(desc.Items); err != nil {
		return nil, err
	}
	desc.Items, err = s.authoritativeCheckoutItems(ctx, desc.Items)
	if err != nil {
		return nil, err
	}
	if err := s.enforcePurchaseLimits(ctx, buyer, desc.Items); err != nil {
		return nil, err
	}
	if desc.Provider == "" {
		desc.Provider = "alipay"
	}
	enabled, err := s.PaymentMethodEnabled(ctx, desc.Provider)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, commerceerr.InvalidRequest("payment provider is not enabled")
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
	if existing, err := s.reusableCheckout(ctx, buyer, desc, total, currency); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
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

func (s *Service) reusableCheckout(ctx context.Context, buyer *model.Buyer, desc CheckoutDesc, amountCents int, currency string) (*model.Order, error) {
	if len(desc.Items) != 1 {
		return nil, nil
	}
	item := desc.Items[0]
	if strings.TrimSpace(item.VariantID) == "" {
		return nil, nil
	}
	return s.db.FindReusableCheckout(
		ctx,
		strings.TrimSpace(buyer.BuyerSub),
		normalizeEmail(buyer.BuyerEmail),
		strings.TrimSpace(desc.Provider),
		strings.TrimSpace(item.VariantID),
		amountCents,
		currency,
		time.Now().UTC().Add(-reusableCheckoutWindow),
	)
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
	if err := validateCheckoutItems(desc.Items); err != nil {
		return nil, err
	}
	desc.Items, err = s.authoritativeCheckoutItems(ctx, desc.Items)
	if err != nil {
		return nil, err
	}
	if err := s.enforcePurchaseLimits(ctx, buyer, desc.Items); err != nil {
		return nil, err
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
	grantAccess := deliveryBundleAccessRules(first.DeliveryRefSnapshot, now.Time, s.delivery.TTL)
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
		fulfilled, err := s.db.FulfillCheckoutTx(ctx, tx, o.ID, "POINTS-"+o.OrderNo, now)
		if err != nil {
			return err
		}
		if !fulfilled {
			return commerceerr.OrderInvalidState(o.Status, model.OrderStatusFulfilled)
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
			ExpiresAt: grantAccess.ExpiresAt, MaxDownloads: grantAccess.MaxDownloads,
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

func (s *Service) ClaimFreeCheckout(ctx context.Context, desc CheckoutDesc) (*FreeCheckoutResult, error) {
	buyer, err := s.resolveBuyer(ctx, desc)
	if err != nil {
		return nil, err
	}
	if err := validateCheckoutItems(desc.Items); err != nil {
		return nil, err
	}
	desc.Items, err = s.authoritativeCheckoutItems(ctx, desc.Items)
	if err != nil {
		return nil, err
	}
	if err := s.enforcePurchaseLimits(ctx, buyer, desc.Items); err != nil {
		return nil, err
	}

	var (
		items          []*model.OrderItem
		firstProductID string
		currency       = "CNY"
	)
	for _, in := range desc.Items {
		if in.Quantity <= 0 {
			in.Quantity = 1
		}
		if in.PriceCents != 0 || in.PointsCost != 0 {
			return nil, commerceerr.InvalidRequest("free checkout only supports zero-price items")
		}
		if in.Currency != "" {
			currency = in.Currency
		}
		p := &model.Product{
			SiteKey: in.SiteKey, ExternalID: in.ExternalID, Kind: model.ProductKindFree,
			Title: in.Title, PriceCents: 0, PointsCost: 0, Currency: currency, Status: model.ProductStatusActive,
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
			Quantity: in.Quantity, UnitPriceCents: 0, UnitPointsCost: 0,
			Currency: currency, DeliveryKindSnapshot: defaultString(in.DeliveryKind, "asset_file"),
			DeliveryRefSnapshot: in.DeliveryRef,
		})
	}

	rawToken, tokenHash, err := newDeliveryToken()
	if err != nil {
		return nil, err
	}
	o := &model.Order{
		OrderNo: NewOrderNo(), Sub: buyer.BuyerSub, ProductID: firstProductID, AmountCents: 0, Currency: currency,
		Status: model.OrderStatusPaying, Gateway: model.ProductKindFree, BuyerID: buyer.ID, BuyerSub: buyer.BuyerSub, BuyerEmail: buyer.BuyerEmail,
		PaymentProvider: model.ProductKindFree, DeliveryState: model.DeliveryStatePending,
	}
	now := gtime.New(time.Now())
	first := items[0]
	grantAccess := deliveryBundleAccessRules(first.DeliveryRefSnapshot, now.Time, s.delivery.TTL)
	err = s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if err := s.db.InsertCheckoutOrderTx(ctx, tx, o, items); err != nil {
			return err
		}
		fulfilled, err := s.db.FulfillCheckoutTx(ctx, tx, o.ID, "FREE-"+o.OrderNo, now)
		if err != nil {
			return err
		}
		if !fulfilled {
			return commerceerr.OrderInvalidState(o.Status, model.OrderStatusFulfilled)
		}
		if err := s.db.InsertPaymentEventTx(ctx, tx, &model.PaymentEvent{
			OrderID: o.ID, Provider: model.ProductKindFree, EventType: "claim", ProviderEventID: o.OrderNo,
			AmountCents: 0, Success: true, Message: "free checkout claimed",
		}); err != nil {
			return err
		}
		if o.BuyerSub != "" {
			for _, item := range items {
				if err := s.db.InsertEntitlementTx(ctx, tx, &model.Entitlement{
					Sub: itemBuyerSub(o), ProductID: item.ProductID, Source: model.EntitlementSourceFree, OrderID: &o.ID,
				}); err != nil {
					return err
				}
			}
		}
		return s.db.InsertDeliveryGrantTx(ctx, tx, &model.DeliveryGrant{
			OrderID: o.ID, OrderItemID: first.ID, BuyerSub: o.BuyerSub, BuyerEmail: o.BuyerEmail,
			TokenHash: tokenHash, DeliveryRef: first.DeliveryRefSnapshot, State: "active",
			ExpiresAt: grantAccess.ExpiresAt, MaxDownloads: grantAccess.MaxDownloads,
		})
	})
	if err != nil {
		return nil, err
	}
	o.Status = model.OrderStatusFulfilled
	o.DeliveryState = model.DeliveryStateGranted
	paidAt := now.Time
	o.PaidAt = &paidAt
	o.FulfilledAt = &paidAt
	s.sendDeliveryMail(ctx, o, first, rawToken)
	return &FreeCheckoutResult{
		Order: o,
		Grant: &CheckoutGrantResult{Token: rawToken, State: model.DeliveryStateGranted, DeliveryRef: first.DeliveryRefSnapshot},
	}, nil
}

func validateCheckoutItems(items []CheckoutItemDesc) error {
	if len(items) == 0 {
		return commerceerr.NotifyInvalid("checkout requires at least one item")
	}
	if len(items) > 20 {
		return commerceerr.NotifyInvalid("checkout supports at most 20 items")
	}
	return nil
}

func (s *Service) authoritativeCheckoutItems(ctx context.Context, items []CheckoutItemDesc) ([]CheckoutItemDesc, error) {
	if s.currentCheckout == nil {
		return items, nil
	}
	out := make([]CheckoutItemDesc, 0, len(items))
	for _, item := range items {
		quantity := item.Quantity
		if quantity <= 0 {
			quantity = 1
		}
		current, err := s.currentCheckout.CurrentCheckoutItem(ctx, CurrentCheckoutItemInput{
			SiteKey:    strings.TrimSpace(item.SiteKey),
			ExternalID: strings.TrimSpace(item.ExternalID),
			VariantID:  strings.TrimSpace(item.VariantID),
		})
		if err != nil {
			return nil, commerceerr.InvalidRequest("checkout item is not available")
		}
		out = append(out, CheckoutItemDesc{
			SiteKey:               defaultString(strings.TrimSpace(current.SiteKey), strings.TrimSpace(item.SiteKey)),
			ExternalID:            defaultString(strings.TrimSpace(current.ExternalID), strings.TrimSpace(item.ExternalID)),
			VariantID:             defaultString(strings.TrimSpace(current.VariantID), strings.TrimSpace(item.VariantID)),
			Title:                 strings.TrimSpace(current.Title),
			VariantTitle:          strings.TrimSpace(current.VariantTitle),
			SKU:                   strings.TrimSpace(current.SKU),
			PriceCents:            current.PriceCents,
			PointsCost:            current.PointsCost,
			Currency:              defaultString(strings.TrimSpace(current.Currency), "CNY"),
			DeliveryKind:          defaultString(strings.TrimSpace(current.DeliveryKind), "asset_file"),
			DeliveryRef:           strings.TrimSpace(current.DeliveryRef),
			PurchaseLimitPerBuyer: current.PurchaseLimitPerBuyer,
			Quantity:              quantity,
		})
	}
	return out, nil
}

func (s *Service) enforcePurchaseLimits(ctx context.Context, buyer *model.Buyer, items []CheckoutItemDesc) error {
	for _, item := range items {
		if item.PurchaseLimitPerBuyer <= 0 {
			continue
		}
		quantity := item.Quantity
		if quantity <= 0 {
			quantity = 1
		}
		purchased, err := s.db.CompletedCheckoutQuantityByVariant(
			ctx,
			strings.TrimSpace(buyer.BuyerSub),
			normalizeEmail(buyer.BuyerEmail),
			strings.TrimSpace(item.VariantID),
		)
		if err != nil {
			return err
		}
		if purchased+quantity > item.PurchaseLimitPerBuyer {
			return commerceerr.NotifyInvalid("该虚拟商品已购买，请在我的订单中查看交付")
		}
	}
	return nil
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
	grantAccess := deliveryBundleAccessRules(first.DeliveryRefSnapshot, now.Time, s.delivery.TTL)
	settled := false
	err = s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		fulfilled, err := s.db.FulfillCheckoutTx(ctx, tx, o.ID, providerTxID, now)
		if err != nil {
			return err
		}
		if !fulfilled {
			return nil
		}
		settled = true
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
			ExpiresAt: grantAccess.ExpiresAt, MaxDownloads: grantAccess.MaxDownloads,
		})
	})
	if err != nil {
		return nil, err
	}
	if !settled {
		return &CheckoutGrantResult{State: model.DeliveryStateGranted}, nil
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

func (s *Service) RecordPaymentFailure(ctx context.Context, orderNo, provider, eventType, providerEventID string, amountCents int, message string) error {
	o, err := s.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	provider = defaultString(strings.TrimSpace(provider), o.PaymentProvider)
	eventType = defaultString(strings.TrimSpace(eventType), "payment")
	return s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		return s.db.InsertPaymentEventTx(ctx, tx, &model.PaymentEvent{
			OrderID:         o.ID,
			Provider:        provider,
			EventType:       eventType,
			ProviderEventID: strings.TrimSpace(providerEventID),
			AmountCents:     amountCents,
			Success:         false,
			Message:         strings.TrimSpace(message),
		})
	})
}

func (s *Service) PaymentMethods(ctx context.Context) ([]PaymentMethodConfig, error) {
	rows, err := s.db.PaymentMethods(ctx)
	if err != nil {
		return nil, err
	}
	byProvider := make(map[string]*model.PaymentMethod, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		byProvider[strings.TrimSpace(row.Provider)] = row
	}
	methods := make([]PaymentMethodConfig, 0, len(paymentMethodOrder))
	for _, provider := range paymentMethodOrder {
		def := paymentMethodDefinitions[provider]
		if row := byProvider[provider]; row != nil {
			def.Enabled = row.Enabled
			def.SortOrder = row.SortOrder
			if strings.TrimSpace(row.DisplayName) != "" {
				def.Label = row.DisplayName
			}
		}
		methods = append(methods, def)
	}
	sort.SliceStable(methods, func(i, j int) bool {
		if methods[i].SortOrder == methods[j].SortOrder {
			return methods[i].Provider < methods[j].Provider
		}
		return methods[i].SortOrder < methods[j].SortOrder
	})
	return methods, nil
}

func (s *Service) PaymentMethodEnabled(ctx context.Context, provider string) (bool, error) {
	provider = strings.TrimSpace(provider)
	if _, ok := paymentMethodDefinitions[provider]; !ok {
		return false, nil
	}
	methods, err := s.PaymentMethods(ctx)
	if err != nil {
		return false, err
	}
	for _, method := range methods {
		if method.Provider == provider {
			return method.Enabled, nil
		}
	}
	return false, nil
}

func (s *Service) SavePaymentMethods(ctx context.Context, inputs []PaymentMethodInput) ([]PaymentMethodConfig, error) {
	seen := map[string]bool{}
	for _, input := range inputs {
		provider := strings.TrimSpace(input.Provider)
		def, ok := paymentMethodDefinitions[provider]
		if !ok {
			return nil, commerceerr.InvalidRequest("unsupported payment provider")
		}
		if seen[provider] {
			return nil, commerceerr.InvalidRequest("duplicate payment provider")
		}
		seen[provider] = true
		label := strings.TrimSpace(input.Label)
		if label == "" {
			label = def.Label
		}
		sortOrder := input.SortOrder
		if sortOrder < 0 {
			return nil, commerceerr.InvalidRequest("sort order cannot be negative")
		}
		if sortOrder == 0 {
			sortOrder = def.SortOrder
		}
		if err := s.db.UpsertPaymentMethod(ctx, &model.PaymentMethod{
			Provider: provider, Enabled: input.Enabled, DisplayName: label, SortOrder: sortOrder,
		}); err != nil {
			return nil, err
		}
	}
	return s.PaymentMethods(ctx)
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

func (s *Service) CancelCheckout(ctx context.Context, orderNo, buyerSub, buyerEmail string) (*model.Order, error) {
	o, err := s.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if !canViewCheckout(o, buyerSub, buyerEmail) {
		return nil, commerceerr.Forbidden()
	}
	if o.Status == model.OrderStatusPending || o.Status == model.OrderStatusPaying {
		if err := s.db.UpdateOrderStatus(ctx, o.ID, model.OrderStatusCancelled); err != nil {
			return nil, err
		}
		o, err = s.GetOrderByNo(ctx, orderNo)
		if err != nil {
			return nil, err
		}
	}
	return o, nil
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

func (s *Service) ListOrders(ctx context.Context, status, q string, limit, offset int) ([]*model.Order, int, error) {
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

func (s *Service) CheckoutStatus(ctx context.Context, orderNo, buyerSub, buyerEmail string) (*CheckoutStatusResult, error) {
	order, err := s.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if !canViewCheckout(order, buyerSub, buyerEmail) {
		return nil, commerceerr.Forbidden()
	}
	grants, err := s.db.DeliveryGrantsByOrderID(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	var grant *model.DeliveryGrant
	for _, item := range grants {
		if item.State == "active" {
			grant = item
			break
		}
		if grant == nil {
			grant = item
		}
	}
	return &CheckoutStatusResult{Order: order, Grant: grant}, nil
}

func canViewCheckout(order *model.Order, buyerSub, buyerEmail string) bool {
	if order == nil {
		return false
	}
	if strings.TrimSpace(order.BuyerSub) != "" && strings.TrimSpace(buyerSub) == strings.TrimSpace(order.BuyerSub) {
		return true
	}
	if normalizeEmail(order.BuyerEmail) != "" && normalizeEmail(buyerEmail) == normalizeEmail(order.BuyerEmail) {
		return true
	}
	return false
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
	grantAccess := deliveryBundleAccessRules(first.DeliveryRefSnapshot, time.Now().UTC(), s.delivery.TTL)
	err = s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if err := s.db.InsertDeliveryGrantTx(ctx, tx, &model.DeliveryGrant{
			OrderID: order.ID, OrderItemID: first.ID, BuyerSub: order.BuyerSub, BuyerEmail: order.BuyerEmail,
			TokenHash: tokenHash, DeliveryRef: first.DeliveryRefSnapshot, State: "active",
			ExpiresAt: grantAccess.ExpiresAt, MaxDownloads: grantAccess.MaxDownloads,
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

func (s *Service) Purchases(ctx context.Context, filter PurchaseFilter, limit, offset int) ([]*DeliveryResult, int, error) {
	filter = PurchaseFilter{Sub: strings.TrimSpace(filter.Sub), Q: strings.TrimSpace(filter.Q), State: strings.TrimSpace(filter.State)}
	grants, total, err := s.db.DeliveryGrantsByBuyerSub(ctx, filter.Sub, filter.Q, filter.State, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*DeliveryResult, 0, len(grants))
	for _, grant := range grants {
		res, err := s.deliveryResult(ctx, grant)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, res)
	}
	return out, total, nil
}

func (s *Service) PurchaseByOrder(ctx context.Context, buyerSub, orderNo string) (*DeliveryResult, error) {
	order, err := s.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(order.BuyerSub) == "" || strings.TrimSpace(order.BuyerSub) != strings.TrimSpace(buyerSub) {
		return nil, commerceerr.Forbidden()
	}
	grants, err := s.db.DeliveryGrantsByOrderID(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	for _, grant := range grants {
		if grant.State == "active" && (grant.ExpiresAt == nil || time.Now().UTC().Before(*grant.ExpiresAt)) {
			return s.deliveryResult(ctx, grant)
		}
	}
	return nil, commerceerr.OrderNotFound("delivery")
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
	if err := s.recordDeliveryDownload(ctx, delivery.Grant); err != nil {
		return DeliveryDownloadResult{}, err
	}
	download, err := s.resolveAssetDelivery(ctx, delivery, expiresAt)
	if err != nil {
		s.rollbackDeliveryDownload(ctx, delivery.Grant)
		return DeliveryDownloadResult{}, err
	}
	return download, nil
}

func (s *Service) ResolvePurchaseDownload(ctx context.Context, buyerSub, orderNo, assetID string) (DeliveryDownloadResult, error) {
	delivery, err := s.PurchaseByOrder(ctx, buyerSub, orderNo)
	if err != nil {
		return DeliveryDownloadResult{}, err
	}
	if err := s.recordDeliveryDownload(ctx, delivery.Grant); err != nil {
		return DeliveryDownloadResult{}, err
	}
	rules := deliveryBundleAccessRules(delivery.Grant.DeliveryRef, time.Now().UTC(), s.delivery.TTL)
	expiresAt := time.Now().UTC().Add(rules.DownloadTTL)
	if delivery.Grant.ExpiresAt != nil && delivery.Grant.ExpiresAt.Before(expiresAt) {
		expiresAt = *delivery.Grant.ExpiresAt
	}
	download, err := s.resolveAssetDelivery(ctx, delivery, expiresAt, assetID)
	if err != nil {
		s.rollbackDeliveryDownload(ctx, delivery.Grant)
		return DeliveryDownloadResult{}, err
	}
	return download, nil
}

func (s *Service) recordDeliveryDownload(ctx context.Context, grant *model.DeliveryGrant) error {
	if grant == nil || strings.TrimSpace(grant.ID) == "" {
		return commerceerr.OrderNotFound("delivery")
	}
	ok, err := s.db.RecordDeliveryDownload(ctx, grant.ID, grant.MaxDownloads)
	if err != nil {
		return err
	}
	if !ok {
		return commerceerr.InvalidRequest("download limit exceeded")
	}
	grant.DownloadCount++
	return nil
}

func (s *Service) rollbackDeliveryDownload(ctx context.Context, grant *model.DeliveryGrant) {
	if grant == nil || strings.TrimSpace(grant.ID) == "" {
		return
	}
	if err := s.db.RollbackDeliveryDownload(ctx, grant.ID); err == nil && grant.DownloadCount > 0 {
		grant.DownloadCount--
	}
}

func (s *Service) resolveAssetDelivery(ctx context.Context, delivery *DeliveryResult, expiresAt time.Time, requestedAssetID ...string) (DeliveryDownloadResult, error) {
	if delivery == nil || delivery.Grant == nil {
		return DeliveryDownloadResult{}, commerceerr.OrderNotFound("delivery")
	}
	kind := "asset_file"
	if delivery.Item != nil && strings.TrimSpace(delivery.Item.DeliveryKindSnapshot) != "" {
		kind = strings.TrimSpace(delivery.Item.DeliveryKindSnapshot)
	}
	deliveryRef := delivery.Grant.DeliveryRef
	if kind == "bundle" && deliveryBundleUpdatePolicy(delivery.Grant.DeliveryRef) == "latest" {
		current, err := s.currentDeliveryRef(ctx, delivery)
		if err != nil {
			if _, snapshotErr := assetIDFromDeliveryBundle(delivery.Grant.DeliveryRef, firstString(requestedAssetID)); snapshotErr != nil {
				return DeliveryDownloadResult{}, err
			}
		} else if strings.TrimSpace(current.DeliveryRef) != "" {
			kind = defaultString(current.DeliveryKind, kind)
			deliveryRef = current.DeliveryRef
		}
	}
	if kind == "bundle" {
		resolved, err := assetIDFromDeliveryBundle(deliveryRef, firstString(requestedAssetID))
		if err != nil {
			return DeliveryDownloadResult{}, err
		}
		deliveryRef = resolved
	} else if kind != "asset_file" {
		return DeliveryDownloadResult{}, commerceerr.InvalidRequest("delivery is not an asset file")
	} else if requested := strings.TrimSpace(firstString(requestedAssetID)); requested != "" && strings.TrimSpace(deliveryRef) != requested {
		return DeliveryDownloadResult{}, commerceerr.Forbidden()
	}
	res := DeliveryDownloadResult{DeliveryRef: deliveryRef, ExpiresAt: expiresAt}
	if s.assetDelivery == nil || strings.TrimSpace(deliveryRef) == "" {
		return res, nil
	}
	assetOut, err := s.assetDelivery.CreateDelivery(ctx, AssetDeliveryInput{
		AssetID:   deliveryRef,
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

func (s *Service) currentDeliveryRef(ctx context.Context, delivery *DeliveryResult) (CurrentDeliveryResult, error) {
	if s.currentDelivery == nil || delivery == nil || delivery.Item == nil {
		return CurrentDeliveryResult{}, nil
	}
	return s.currentDelivery.CurrentDelivery(ctx, CurrentDeliveryInput{
		SiteKey:    strings.TrimSpace(delivery.Item.SiteKey),
		ExternalID: strings.TrimSpace(delivery.Item.ExternalID),
		VariantID:  strings.TrimSpace(delivery.Item.VariantID),
	})
}

func deliveryBundleUpdatePolicy(raw string) string {
	var payload struct {
		UpdatePolicy string `json:"updatePolicy"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	policy := strings.TrimSpace(payload.UpdatePolicy)
	if policy == "" {
		return "snapshot"
	}
	return policy
}

func deliveryBundleAccessRules(raw string, now time.Time, defaultTTL time.Duration) deliveryAccessRulesResult {
	if defaultTTL <= 0 {
		defaultTTL = 15 * time.Minute
	}
	out := deliveryAccessRulesResult{DownloadTTL: defaultTTL}
	var payload struct {
		Access struct {
			ExpiresDays        int `json:"expiresDays"`
			MaxDownloads       int `json:"maxDownloads"`
			DownloadLinkTTLMin int `json:"downloadLinkTTLMin"`
		} `json:"access"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return out
	}
	if payload.Access.ExpiresDays > 0 {
		expiresAt := now.Add(time.Duration(payload.Access.ExpiresDays) * 24 * time.Hour)
		out.ExpiresAt = &expiresAt
	}
	if payload.Access.MaxDownloads > 0 {
		out.MaxDownloads = payload.Access.MaxDownloads
	}
	if payload.Access.DownloadLinkTTLMin > 0 {
		out.DownloadTTL = time.Duration(payload.Access.DownloadLinkTTLMin) * time.Minute
	}
	return out
}

func assetIDFromDeliveryBundle(raw, requested string) (string, error) {
	var payload struct {
		Items []struct {
			Kind    string `json:"kind"`
			AssetID string `json:"assetId"`
			Enabled *bool  `json:"enabled"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", commerceerr.InvalidRequest("invalid delivery bundle")
	}
	assets := make([]string, 0, len(payload.Items))
	for _, item := range payload.Items {
		if item.Enabled != nil && !*item.Enabled {
			continue
		}
		if item.Kind != "asset_file" || strings.TrimSpace(item.AssetID) == "" {
			continue
		}
		assets = append(assets, strings.TrimSpace(item.AssetID))
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if len(assets) == 1 {
			return assets[0], nil
		}
		return "", commerceerr.InvalidRequest("assetId is required for bundled delivery")
	}
	for _, assetID := range assets {
		if assetID == requested {
			return requested, nil
		}
	}
	return "", commerceerr.Forbidden()
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
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
	rules := deliveryBundleAccessRules(res.Grant.DeliveryRef, time.Now().UTC(), s.delivery.TTL)
	expiresAt := time.Now().UTC().Add(rules.DownloadTTL)
	if res.Grant.ExpiresAt != nil && res.Grant.ExpiresAt.Before(expiresAt) {
		expiresAt = *res.Grant.ExpiresAt
	}
	url := s.signedDeliveryURLWithExpiry(res.Token, res.Grant.DeliveryRef, expiresAt)
	res.DownloadURL = url
	res.DownloadExpiresAt = &expiresAt
}

func (s *Service) signedDeliveryURL(token, deliveryRef string) (string, time.Time) {
	expiresAt := time.Now().UTC().Add(s.delivery.TTL)
	return s.signedDeliveryURLWithExpiry(token, deliveryRef, expiresAt), expiresAt
}

func (s *Service) signedDeliveryURLWithExpiry(token, deliveryRef string, expiresAt time.Time) string {
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	sig := s.deliverySignature(token, exp, deliveryRef)
	q := url.Values{}
	q.Set("exp", exp)
	q.Set("sig", sig)
	path := "/api/v1/delivery/" + url.PathEscape(token) + "/download"
	return s.delivery.PublicBaseURL + path + "?" + q.Encode()
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
