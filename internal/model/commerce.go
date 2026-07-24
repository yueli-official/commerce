// Package model holds the domain structs and constants for the commerce service.
package model

import "time"

// Order status constants.
const (
	OrderStatusPending   = "pending"
	OrderStatusPaying    = "paying"
	OrderStatusPaid      = "paid"
	OrderStatusCancelled = "cancelled"
	OrderStatusDraft     = "draft"
	OrderStatusFulfilled = "fulfilled"
	OrderStatusRefunding = "refunding"
	OrderStatusRefunded  = "refunded"
	OrderStatusFailed    = "failed"
)

// Product status constants.
const (
	ProductStatusActive   = "active"
	ProductStatusInactive = "inactive"
)

// Product kind constants.
const (
	ProductKindPaid   = "paid"
	ProductKindPoints = "points"
	ProductKindFree   = "free"
)

// Entitlement source constants.
const (
	EntitlementSourceOrder  = "order"  // paid purchase
	EntitlementSourcePoints = "points" // points redemption
	EntitlementSourceFree   = "free"   // zero-price checkout
	EntitlementSourceGrant  = "grant"  // admin/activity grant
)

// Credits ledger source constants.
const (
	CreditsSourceCheckin = "checkin"
	CreditsSourceRedeem  = "redeem"
	CreditsSourceGrant   = "grant"
)

const (
	BuyerKindUser  = "user"
	BuyerKindGuest = "guest"
)

const (
	DeliveryStatePending = "pending"
	DeliveryStateGranted = "granted"
	DeliveryStateRevoked = "revoked"
	DeliveryStateFailed  = "failed"
)

// Product is a purchasable item tied to a site. A `paid` product is priced in
// price_cents; a `points` product is priced in points_cost.
type Product struct {
	ID         string    `json:"id"         orm:"id"`
	SiteKey    string    `json:"siteKey"    orm:"site_key"`
	ExternalID string    `json:"externalId" orm:"external_id"`
	Kind       string    `json:"kind"       orm:"kind"`
	Title      string    `json:"title"      orm:"title"`
	PriceCents int       `json:"priceCents" orm:"price_cents"`
	PointsCost int       `json:"pointsCost" orm:"points_cost"`
	Currency   string    `json:"currency"   orm:"currency"`
	Status     string    `json:"status"     orm:"status"`
	CreatedAt  time.Time `json:"createdAt"  orm:"created_at"`
	UpdatedAt  time.Time `json:"updatedAt"  orm:"updated_at"`
}

// CheckinRecord is one subscriber's check-in on a given day.
type CheckinRecord struct {
	ID            string    `json:"id"            orm:"id"`
	Sub           string    `json:"sub"           orm:"sub"`
	CheckinDate   string    `json:"checkinDate"   orm:"checkin_date"`
	Streak        int       `json:"streak"        orm:"streak"`
	PointsAwarded int       `json:"pointsAwarded" orm:"points_awarded"`
	CreatedAt     time.Time `json:"createdAt"     orm:"created_at"`
}

// LedgerEntry is one append-only points movement (+earn / -spend).
type LedgerEntry struct {
	ID        string    `json:"id"        orm:"id"`
	Sub       string    `json:"sub"       orm:"sub"`
	Delta     int       `json:"delta"     orm:"delta"`
	Source    string    `json:"source"    orm:"source"`
	Ref       string    `json:"ref"       orm:"ref"`
	CreatedAt time.Time `json:"createdAt" orm:"created_at"`
}

// Order is a single purchase transaction.
type Order struct {
	ID               string     `json:"id"                orm:"id"`
	OrderNo          string     `json:"orderNo"           orm:"order_no"`
	Sub              string     `json:"sub"               orm:"sub"`
	ProductID        string     `json:"productId"         orm:"product_id"`
	AmountCents      int        `json:"amountCents"       orm:"amount_cents"`
	Currency         string     `json:"currency"          orm:"currency"`
	Status           string     `json:"status"            orm:"status"`
	Gateway          string     `json:"gateway"           orm:"gateway"`
	ProviderTxID     string     `json:"providerTxId"      orm:"provider_tx_id"`
	PaidAt           *time.Time `json:"paidAt"            orm:"paid_at"`
	BuyerID          string     `json:"buyerId"           orm:"buyer_id"`
	BuyerSub         string     `json:"buyerSub"          orm:"buyer_sub"`
	BuyerEmail       string     `json:"buyerEmail"        orm:"buyer_email"`
	PaymentProvider  string     `json:"paymentProvider"   orm:"payment_provider"`
	PaymentSessionID string     `json:"paymentSessionId"  orm:"payment_session_id"`
	PaymentExpiresAt *time.Time `json:"paymentExpiresAt"  orm:"payment_expires_at"`
	PaymentState     string     `json:"paymentState"      orm:"payment_state"`
	RefundedAmount   int        `json:"refundedAmountCents" orm:"refunded_amount_cents"`
	DisputeState     string     `json:"disputeState"      orm:"dispute_state"`
	ReturnURL        string     `json:"returnUrl"         orm:"return_url"`
	CancelURL        string     `json:"cancelUrl"         orm:"cancel_url"`
	FulfilledAt      *time.Time `json:"fulfilledAt"       orm:"fulfilled_at"`
	DeliveryState    string     `json:"deliveryState"     orm:"delivery_state"`
	CreatedAt        time.Time  `json:"createdAt"         orm:"created_at"`
	UpdatedAt        time.Time  `json:"updatedAt"         orm:"updated_at"`
}

// Entitlement records that a subscriber may access a product.
type Entitlement struct {
	ID        string     `json:"id"        orm:"id"`
	Sub       string     `json:"sub"       orm:"sub"`
	ProductID string     `json:"productId" orm:"product_id"`
	Source    string     `json:"source"    orm:"source"`
	OrderID   *string    `json:"orderId"   orm:"order_id"`
	GrantedAt time.Time  `json:"grantedAt" orm:"granted_at"`
	ExpiresAt *time.Time `json:"expiresAt" orm:"expires_at"`
}

type Buyer struct {
	ID              string    `json:"id"              orm:"id"`
	Kind            string    `json:"kind"            orm:"kind"`
	BuyerSub        string    `json:"buyerSub"        orm:"buyer_sub"`
	BuyerEmail      string    `json:"buyerEmail"      orm:"buyer_email"`
	EmailNormalized string    `json:"emailNormalized" orm:"email_normalized"`
	CreatedAt       time.Time `json:"createdAt"       orm:"created_at"`
	UpdatedAt       time.Time `json:"updatedAt"       orm:"updated_at"`
}

type OrderItem struct {
	ID                   string    `json:"id"                   orm:"id"`
	OrderID              string    `json:"orderId"              orm:"order_id"`
	SiteKey              string    `json:"siteKey"              orm:"site_key"`
	ExternalID           string    `json:"externalId"           orm:"external_id"`
	ProductID            string    `json:"productId"            orm:"product_id"`
	VariantID            string    `json:"variantId"            orm:"variant_id"`
	TitleSnapshot        string    `json:"titleSnapshot"        orm:"title_snapshot"`
	VariantTitleSnapshot string    `json:"variantTitleSnapshot" orm:"variant_title_snapshot"`
	SKUSnapshot          string    `json:"skuSnapshot"          orm:"sku_snapshot"`
	Quantity             int       `json:"quantity"             orm:"quantity"`
	UnitPriceCents       int       `json:"unitPriceCents"       orm:"unit_price_cents"`
	UnitPointsCost       int       `json:"unitPointsCost"       orm:"unit_points_cost"`
	Currency             string    `json:"currency"             orm:"currency"`
	DeliveryKindSnapshot string    `json:"deliveryKindSnapshot" orm:"delivery_kind_snapshot"`
	DeliveryRefSnapshot  string    `json:"deliveryRefSnapshot"  orm:"delivery_ref_snapshot"`
	CreatedAt            time.Time `json:"createdAt"            orm:"created_at"`
}

type PaymentEvent struct {
	ID              string    `json:"id"              orm:"id"`
	OrderID         string    `json:"orderId"         orm:"order_id"`
	Provider        string    `json:"provider"        orm:"provider"`
	EventType       string    `json:"eventType"       orm:"event_type"`
	ProviderEventID string    `json:"providerEventId" orm:"provider_event_id"`
	RawHash         string    `json:"rawHash"         orm:"raw_hash"`
	AmountCents     int       `json:"amountCents"     orm:"amount_cents"`
	Success         bool      `json:"success"         orm:"success"`
	Message         string    `json:"message"         orm:"message"`
	CreatedAt       time.Time `json:"createdAt"       orm:"created_at"`
}

type DeliveryGrant struct {
	ID            string     `json:"id"          orm:"id"`
	OrderID       string     `json:"orderId"     orm:"order_id"`
	OrderItemID   string     `json:"orderItemId" orm:"order_item_id"`
	BuyerSub      string     `json:"buyerSub"    orm:"buyer_sub"`
	BuyerEmail    string     `json:"buyerEmail"  orm:"buyer_email"`
	TokenHash     string     `json:"tokenHash"   orm:"token_hash"`
	DeliveryRef   string     `json:"deliveryRef" orm:"delivery_ref"`
	State         string     `json:"state"       orm:"state"`
	MaxDownloads  int        `json:"maxDownloads"  orm:"max_downloads"`
	DownloadCount int        `json:"downloadCount" orm:"download_count"`
	CreatedAt     time.Time  `json:"createdAt"   orm:"created_at"`
	ExpiresAt     *time.Time `json:"expiresAt"   orm:"expires_at"`
	RevokedAt     *time.Time `json:"revokedAt"   orm:"revoked_at"`
}

type PaymentMethod struct {
	Provider    string    `json:"provider"    orm:"provider"`
	Enabled     bool      `json:"enabled"     orm:"enabled"`
	DisplayName string    `json:"displayName" orm:"display_name"`
	SortOrder   int       `json:"sortOrder"   orm:"sort_order"`
	CreatedAt   time.Time `json:"createdAt"   orm:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt"   orm:"updated_at"`
}
