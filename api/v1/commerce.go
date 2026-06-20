// Package v1 declares the g.Meta request/response contracts for the commerce
// service HTTP API.  Controllers embed these types to satisfy GoFrame's
// struct-handler routing.
package v1

import "github.com/gogf/gf/v2/frame/g"

// ─── POST /api/v1/orders ────────────────────────────────────────────────────

// CreateOrderReq is the request body for CreateOrder. A `paid` order is priced in
// priceCents (+ currency); a `points` order is priced in pointsCost. The per-kind
// required fields are validated in the handler.
type CreateOrderReq struct {
	g.Meta     `path:"/api/v1/orders" method:"POST" tags:"Commerce" summary:"Create a paid order (payUrl) or redeem points (entitled)"`
	SiteKey    string `json:"siteKey"     v:"required|length:1,128"`
	ExternalID string `json:"externalId"  v:"required|length:1,128"`
	Kind       string `json:"kind"        v:"required|in:paid,points"`
	PriceCents int    `json:"priceCents"`
	PointsCost int    `json:"pointsCost"`
	Title      string `json:"title"       v:"required|length:1,200"`
	Currency   string `json:"currency"`
}

// CreateOrderRes is the response body for CreateOrder. paid → orderNo + payUrl;
// points → entitled (redeemed synchronously) + the remaining balance.
type CreateOrderRes struct {
	OrderNo  string `json:"orderNo,omitempty"`
	PayURL   string `json:"payUrl,omitempty"`
	Entitled bool   `json:"entitled"`
	Balance  *int   `json:"balance,omitempty"`
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
	PriceCents *int   `json:"priceCents,omitempty"`
	PointsCost *int   `json:"pointsCost,omitempty"`
}

// ─── POST /api/v1/checkin · GET /api/v1/checkin/status ──────────────────────

// CheckinReq is the (empty-body) daily check-in request.
type CheckinReq struct {
	g.Meta `path:"/api/v1/checkin" method:"POST" tags:"Commerce" summary:"Daily check-in; credits the streak reward"`
}

// CheckinRes is the response for a check-in.
type CheckinRes struct {
	Date             string `json:"date"`
	Streak           int    `json:"streak"`
	PointsAwarded    int    `json:"pointsAwarded"`
	Balance          int    `json:"balance"`
	AlreadyCheckedIn bool   `json:"alreadyCheckedIn"`
}

// CheckinStatusReq is the read-only check-in status request.
type CheckinStatusReq struct {
	g.Meta `path:"/api/v1/checkin/status" method:"GET" tags:"Commerce" summary:"Today's check-in state (no mutation)"`
}

// CheckinStatusRes is today's check-in state.
type CheckinStatusRes struct {
	CheckedInToday bool `json:"checkedInToday"`
	Streak         int  `json:"streak"`
	Balance        int  `json:"balance"`
}

// ─── GET /api/v1/credits/balance · GET /api/v1/credits/ledger ───────────────

// BalanceReq is the points-balance request.
type BalanceReq struct {
	g.Meta `path:"/api/v1/credits/balance" method:"GET" tags:"Commerce" summary:"Current points balance"`
}

// BalanceRes is the points balance.
type BalanceRes struct {
	Balance int `json:"balance"`
}

// LedgerReq is the paginated ledger request.
type LedgerReq struct {
	g.Meta `path:"/api/v1/credits/ledger" method:"GET" tags:"Commerce" summary:"Points ledger (newest first)"`
	Page   int `p:"page"`
	Size   int `p:"size"`
}

// LedgerEntryView is one ledger row.
type LedgerEntryView struct {
	Delta     int    `json:"delta"`
	Source    string `json:"source"`
	Ref       string `json:"ref"`
	CreatedAt string `json:"createdAt"`
}

// LedgerRes is a page of ledger entries.
type LedgerRes struct {
	Entries []*LedgerEntryView `json:"entries"`
	Total   int                `json:"total"`
	Page    int                `json:"page"`
	Size    int                `json:"size"`
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
