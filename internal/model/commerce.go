// Package model holds the domain structs and constants for the commerce service.
package model

import "time"

// Order status constants.
const (
	OrderStatusPending   = "pending"
	OrderStatusPaying    = "paying"
	OrderStatusPaid      = "paid"
	OrderStatusCancelled = "cancelled"
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
)

// Entitlement source constants.
const (
	EntitlementSourceOrder  = "order"  // paid purchase
	EntitlementSourcePoints = "points" // points redemption
	EntitlementSourceGrant  = "grant"  // admin/activity grant
)

// Credits ledger source constants.
const (
	CreditsSourceCheckin = "checkin"
	CreditsSourceRedeem  = "redeem"
	CreditsSourceGrant   = "grant"
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
	ID           string     `json:"id"           orm:"id"`
	OrderNo      string     `json:"orderNo"      orm:"order_no"`
	Sub          string     `json:"sub"          orm:"sub"`
	ProductID    string     `json:"productId"    orm:"product_id"`
	AmountCents  int        `json:"amountCents"  orm:"amount_cents"`
	Currency     string     `json:"currency"     orm:"currency"`
	Status       string     `json:"status"       orm:"status"`
	Gateway      string     `json:"gateway"      orm:"gateway"`
	ProviderTxID string     `json:"providerTxId" orm:"provider_tx_id"`
	PaidAt       *time.Time `json:"paidAt"       orm:"paid_at"`
	CreatedAt    time.Time  `json:"createdAt"    orm:"created_at"`
	UpdatedAt    time.Time  `json:"updatedAt"    orm:"updated_at"`
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
