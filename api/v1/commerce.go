// Package v1 declares the g.Meta request/response contracts for the commerce
// service HTTP API (spec §4).  Controllers embed these types to satisfy GoFrame's
// struct-handler routing.
package v1

import "github.com/gogf/gf/v2/frame/g"

// ─── POST /api/v1/orders ────────────────────────────────────────────────────

// CreateOrderReq is the request body for CreateOrder.
type CreateOrderReq struct {
	g.Meta     `path:"/api/v1/orders" method:"POST" tags:"Commerce" summary:"Create an order and return a payment URL"`
	SiteKey    string `json:"siteKey"     v:"required"`
	ExternalID string `json:"externalId"  v:"required"`
	Kind       string `json:"kind"        v:"required"`
	PriceCents int    `json:"priceCents"  v:"required|min:1"`
	Title      string `json:"title"       v:"required"`
	Currency   string `json:"currency"    v:"required"`
}

// CreateOrderRes is the response body for CreateOrder.
type CreateOrderRes struct {
	OrderNo string `json:"orderNo"`
	PayURL  string `json:"payUrl"`
}

// ─── GET /api/v1/access ─────────────────────────────────────────────────────

// EntitledReq is the query-param request for Entitled.
type EntitledReq struct {
	g.Meta     `path:"/api/v1/access" method:"GET" tags:"Commerce" summary:"Check whether the authenticated user is entitled to access a resource"`
	SiteKey    string `p:"siteKey"    v:"required"`
	ExternalID string `p:"externalId" v:"required"`
}

// EntitledRes is the response body for Entitled.
type EntitledRes struct {
	Entitled bool            `json:"entitled"`
	Reason   string          `json:"reason"`
	Required *RequiredFields `json:"required,omitempty"`
}

// RequiredFields is the nested object describing what purchase is required.
type RequiredFields struct {
	Kind       string `json:"kind"`
	PriceCents *int   `json:"priceCents"`
}

// ─── POST /api/v1/payments/alipay/notify ────────────────────────────────────
// (Public, no authjwt — handled via raw ghttp.Request in the controller,
// not via struct-based binding, because the response must be plaintext "success".)

// ─── POST /dev/orders/{orderNo}/settle ──────────────────────────────────────

// DevSettleReq is the path-param request for dev settle.
type DevSettleReq struct {
	g.Meta  `path:"/dev/orders/{orderNo}/settle" method:"POST" tags:"Dev" summary:"Simulate a successful payment notify (dev only)"`
	OrderNo string `p:"orderNo" v:"required"`
}

// DevSettleRes is the response body for dev settle (empty data).
type DevSettleRes struct{}
