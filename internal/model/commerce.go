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

// Product is a purchasable item tied to a site (spec §3).
type Product struct {
	ID         string    `json:"id"         orm:"id"`
	SiteKey    string    `json:"siteKey"    orm:"site_key"`
	ExternalID string    `json:"externalId" orm:"external_id"`
	Kind       string    `json:"kind"       orm:"kind"`
	Title      string    `json:"title"      orm:"title"`
	PriceCents int       `json:"priceCents" orm:"price_cents"`
	Currency   string    `json:"currency"   orm:"currency"`
	Status     string    `json:"status"     orm:"status"`
	CreatedAt  time.Time `json:"createdAt"  orm:"created_at"`
	UpdatedAt  time.Time `json:"updatedAt"  orm:"updated_at"`
}

// Order is a single purchase transaction (spec §3).
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

// Entitlement records that a subscriber may access a product (spec §3).
type Entitlement struct {
	ID        string     `json:"id"        orm:"id"`
	Sub       string     `json:"sub"       orm:"sub"`
	ProductID string     `json:"productId" orm:"product_id"`
	Source    string     `json:"source"    orm:"source"`
	OrderID   *string    `json:"orderId"   orm:"order_id"`
	GrantedAt time.Time  `json:"grantedAt" orm:"granted_at"`
	ExpiresAt *time.Time `json:"expiresAt" orm:"expires_at"`
}
