// Package v1 declares the g.Meta request/response contracts for the commerce
// service HTTP API.  Controllers embed these types to satisfy GoFrame's
// struct-handler routing.
package v1

import "github.com/gogf/gf/v2/frame/g"

// ─── POST /api/v1/checkouts ────────────────────────────────────────────────

type CheckoutItemReq struct {
	SiteKey               string `json:"siteKey" v:"required|length:1,128"`
	ExternalID            string `json:"externalId" v:"required|length:1,128"`
	VariantID             string `json:"variantId" v:"required|length:1,128"`
	Title                 string `json:"title" v:"required|length:1,200"`
	VariantTitle          string `json:"variantTitle"`
	SKU                   string `json:"sku"`
	PriceCents            int    `json:"priceCents"`
	PointsCost            int    `json:"pointsCost"`
	Currency              string `json:"currency"`
	DeliveryKind          string `json:"deliveryKind"`
	DeliveryRef           string `json:"deliveryRef"`
	PurchaseLimitPerBuyer int    `json:"purchaseLimitPerBuyer"`
	Quantity              int    `json:"quantity"`
}

type CreateCheckoutReq struct {
	g.Meta     `path:"/api/v1/checkouts" method:"POST" tags:"Commerce Checkout" summary:"Create a virtual-goods checkout for a guest email or logged-in buyer"`
	BuyerEmail string            `json:"buyerEmail"`
	Provider   string            `json:"provider"`
	ReturnURL  string            `json:"returnUrl"`
	CancelURL  string            `json:"cancelUrl"`
	Items      []CheckoutItemReq `json:"items" v:"required"`
}

type CreateCheckoutRes struct {
	OrderNo     string `json:"orderNo"`
	AmountCents int    `json:"amountCents"`
	Currency    string `json:"currency"`
	Provider    string `json:"provider"`
	Method      string `json:"method"`
	PayURL      string `json:"payUrl,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
	QRCode      string `json:"qrCode,omitempty"`
	ClientToken string `json:"clientToken,omitempty"`
}

type CaptureCheckoutReq struct {
	g.Meta    `path:"/api/v1/checkouts/{orderNo}/capture" method:"POST" tags:"Commerce Checkout" summary:"Capture a browser-button checkout session"`
	OrderNo   string `p:"orderNo" v:"required"`
	Provider  string `json:"provider" v:"required|length:1,32"`
	SessionID string `json:"sessionId" v:"required|length:1,256"`
}

type CaptureCheckoutRes struct {
	OrderNo     string `json:"orderNo"`
	Token       string `json:"token,omitempty"`
	DeliveryRef string `json:"deliveryRef,omitempty"`
	State       string `json:"state"`
}

type CheckoutStatusReq struct {
	g.Meta     `path:"/api/v1/checkouts/{orderNo}/status" method:"GET" tags:"Commerce Checkout" summary:"Get checkout payment and delivery status for the buyer"`
	OrderNo    string `p:"orderNo" v:"required"`
	BuyerEmail string `p:"buyerEmail"`
}

type CheckoutStatusRes struct {
	OrderNo       string `json:"orderNo"`
	Status        string `json:"status"`
	DeliveryState string `json:"deliveryState"`
	DeliveryRef   string `json:"deliveryRef,omitempty"`
}

type SyncCheckoutReq struct {
	g.Meta     `path:"/api/v1/checkouts/{orderNo}/sync" method:"POST" tags:"Commerce Checkout" summary:"Query the payment provider and settle a buyer-owned checkout when paid"`
	OrderNo    string `p:"orderNo" v:"required"`
	BuyerEmail string `json:"buyerEmail"`
}

type SyncCheckoutRes = CheckoutStatusRes

type CancelCheckoutReq struct {
	g.Meta     `path:"/api/v1/checkouts/{orderNo}/cancel" method:"POST" tags:"Commerce Checkout" summary:"Cancel a buyer-owned pending checkout"`
	OrderNo    string `p:"orderNo" v:"required"`
	BuyerEmail string `json:"buyerEmail"`
}

type CancelCheckoutRes struct {
	OrderNo       string `json:"orderNo"`
	Status        string `json:"status"`
	DeliveryState string `json:"deliveryState"`
}

type CreatePointsCheckoutReq struct {
	g.Meta     `path:"/api/v1/checkouts/points" method:"POST" tags:"Commerce Checkout" summary:"Redeem a virtual-goods checkout with points"`
	BuyerEmail string            `json:"buyerEmail"`
	Items      []CheckoutItemReq `json:"items" v:"required"`
}

type CreatePointsCheckoutRes struct {
	OrderNo     string `json:"orderNo"`
	Token       string `json:"token,omitempty"`
	DeliveryRef string `json:"deliveryRef,omitempty"`
	State       string `json:"state"`
	Balance     int    `json:"balance"`
}

type CreateFreeCheckoutReq struct {
	g.Meta     `path:"/api/v1/checkouts/free" method:"POST" tags:"Commerce Checkout" summary:"Claim a zero-price virtual-goods checkout"`
	BuyerEmail string            `json:"buyerEmail"`
	Items      []CheckoutItemReq `json:"items" v:"required"`
}

type CreateFreeCheckoutRes struct {
	OrderNo     string `json:"orderNo"`
	Token       string `json:"token,omitempty"`
	DeliveryRef string `json:"deliveryRef,omitempty"`
	State       string `json:"state"`
}

type DeliveryView struct {
	OrderNo           string               `json:"orderNo"`
	BuyerEmail        string               `json:"buyerEmail,omitempty"`
	SiteKey           string               `json:"siteKey,omitempty"`
	ExternalID        string               `json:"externalId,omitempty"`
	VariantID         string               `json:"variantId,omitempty"`
	Title             string               `json:"title"`
	VariantTitle      string               `json:"variantTitle,omitempty"`
	SKU               string               `json:"sku,omitempty"`
	DeliveryKind      string               `json:"deliveryKind"`
	DeliveryRef       string               `json:"deliveryRef"`
	Netdisk           *NetdiskDeliveryView `json:"netdisk,omitempty"`
	DownloadURL       string               `json:"downloadUrl,omitempty"`
	DownloadExpiresAt string               `json:"downloadExpiresAt,omitempty"`
	State             string               `json:"state"`
	CreatedAt         string               `json:"createdAt"`
}

type NetdiskDeliveryView struct {
	Provider    string `json:"provider,omitempty"`
	URL         string `json:"url,omitempty"`
	AccessCode  string `json:"accessCode,omitempty"`
	ExtractCode string `json:"extractCode,omitempty"`
	Note        string `json:"note,omitempty"`
}

type DeliveryByTokenReq struct {
	g.Meta `path:"/api/v1/delivery/{token}" method:"GET" tags:"Commerce Delivery" summary:"Get a delivery grant by token"`
	Token  string `p:"token" v:"required"`
}

type DeliveryByTokenRes struct {
	Delivery DeliveryView `json:"delivery"`
}

type DeliveryDownloadReq struct {
	g.Meta `path:"/api/v1/delivery/{token}/download" method:"GET" tags:"Commerce Delivery" summary:"Validate a signed delivery download handoff"`
	Token  string `p:"token" v:"required"`
	Exp    string `p:"exp" v:"required"`
	Sig    string `p:"sig" v:"required"`
}

type DeliveryDownloadRes struct {
	DeliveryRef string `json:"deliveryRef"`
	URL         string `json:"url,omitempty"`
	ExpiresAt   string `json:"expiresAt"`
}

type MyPurchasesReq struct {
	g.Meta `path:"/api/v1/me/purchases" method:"GET" tags:"Commerce Delivery" summary:"List current user's virtual purchases"`
	Q      string `p:"q"`
	State  string `p:"state"`
	Limit  int    `p:"limit"`
	Offset int    `p:"offset"`
}

type MyPurchasesRes struct {
	Purchases []DeliveryView `json:"purchases"`
	Total     int            `json:"total"`
}

type MyPurchaseByOrderReq struct {
	g.Meta  `path:"/api/v1/me/purchases/{orderNo}" method:"GET" tags:"Commerce Delivery" summary:"Get current user's purchase delivery by order number"`
	OrderNo string `p:"orderNo" v:"required"`
}

type MyPurchaseByOrderRes struct {
	Delivery DeliveryView `json:"delivery"`
}

type MyPurchaseDownloadReq struct {
	g.Meta  `path:"/api/v1/me/purchases/{orderNo}/download" method:"GET" tags:"Commerce Delivery" summary:"Create a download URL for current user's purchase"`
	OrderNo string `p:"orderNo" v:"required"`
	AssetID string `p:"assetId"`
}

// ─── GET /api/v1/payments/methods ─────────────────────────────────────────

type PaymentMethodView struct {
	Provider    string `json:"provider"`
	Label       string `json:"label"`
	Method      string `json:"method"`
	Enabled     bool   `json:"enabled"`
	Registered  bool   `json:"registered"`
	SortOrder   int    `json:"sortOrder"`
	Description string `json:"description,omitempty"`
}

type PublicPaymentMethodsReq struct {
	g.Meta `path:"/api/v1/payments/methods" method:"GET" tags:"Commerce Payments" summary:"List enabled and configured payment methods"`
}

type PublicPaymentMethodsRes struct {
	Methods []PaymentMethodView `json:"methods"`
}

type AdminPaymentMethodsReq struct {
	g.Meta `path:"/api/v1/admin/commerce/payment-methods" method:"GET" tags:"Admin Commerce" summary:"List payment method configuration"`
}

type AdminPaymentMethodsRes struct {
	Methods []PaymentMethodView `json:"methods"`
}

type AdminPaymentMethodInput struct {
	Provider  string `json:"provider" v:"required|length:1,32"`
	Label     string `json:"label"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sortOrder"`
}

type AdminSavePaymentMethodsReq struct {
	g.Meta  `path:"/api/v1/admin/commerce/payment-methods" method:"PUT" tags:"Admin Commerce" summary:"Save payment method configuration"`
	Methods []AdminPaymentMethodInput `json:"methods" v:"required"`
}

type AdminSavePaymentMethodsRes struct {
	Methods []PaymentMethodView `json:"methods"`
}

// ─── GET /api/v1/admin/commerce/orders ─────────────────────────────────────

type AdminOrderItemView struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	VariantTitle string `json:"variantTitle"`
	SKU          string `json:"sku"`
	Quantity     int    `json:"quantity"`
	PriceCents   int    `json:"priceCents"`
	Currency     string `json:"currency"`
	DeliveryKind string `json:"deliveryKind"`
	DeliveryRef  string `json:"deliveryRef"`
}

type AdminPaymentEventView struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	EventType       string `json:"eventType"`
	ProviderEventID string `json:"providerEventId"`
	AmountCents     int    `json:"amountCents"`
	Success         bool   `json:"success"`
	Message         string `json:"message"`
	CreatedAt       string `json:"createdAt"`
}

type AdminDeliveryGrantView struct {
	ID          string `json:"id"`
	OrderItemID string `json:"orderItemId"`
	BuyerSub    string `json:"buyerSub,omitempty"`
	BuyerEmail  string `json:"buyerEmail,omitempty"`
	DeliveryRef string `json:"deliveryRef"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	RevokedAt   string `json:"revokedAt,omitempty"`
}

type AdminOrderView struct {
	ID              string                   `json:"id"`
	OrderNo         string                   `json:"orderNo"`
	Sub             string                   `json:"sub,omitempty"`
	BuyerSub        string                   `json:"buyerSub,omitempty"`
	BuyerEmail      string                   `json:"buyerEmail,omitempty"`
	AmountCents     int                      `json:"amountCents"`
	Currency        string                   `json:"currency"`
	Status          string                   `json:"status"`
	PaymentProvider string                   `json:"paymentProvider"`
	DeliveryState   string                   `json:"deliveryState"`
	CreatedAt       string                   `json:"createdAt"`
	Items           []AdminOrderItemView     `json:"items,omitempty"`
	Events          []AdminPaymentEventView  `json:"events,omitempty"`
	Grants          []AdminDeliveryGrantView `json:"grants,omitempty"`
}

type AdminListOrdersReq struct {
	g.Meta `path:"/api/v1/admin/commerce/orders" method:"GET" tags:"Admin Commerce" summary:"List commerce orders"`
	Q      string `p:"q"`
	Status string `p:"status"`
	Limit  int    `p:"limit"`
	Offset int    `p:"offset"`
}

type AdminListOrdersRes struct {
	Orders []AdminOrderView `json:"orders"`
	Total  int              `json:"total"`
}

type AdminOrderDetailReq struct {
	g.Meta  `path:"/api/v1/admin/commerce/orders/{orderNo}" method:"GET" tags:"Admin Commerce" summary:"Get commerce order detail"`
	OrderNo string `p:"orderNo" v:"required"`
}

type AdminOrderDetailRes struct {
	Order AdminOrderView `json:"order"`
}

type AdminOrderDeliveryResendReq struct {
	g.Meta  `path:"/api/v1/admin/commerce/orders/{orderNo}/delivery/resend" method:"POST" tags:"Admin Commerce" summary:"Resend delivery email"`
	OrderNo string `p:"orderNo" v:"required"`
}

type AdminOrderDeliveryResendRes struct {
	Token       string `json:"token,omitempty"`
	DeliveryRef string `json:"deliveryRef,omitempty"`
}

type AdminOrderDeliveryRevokeReq struct {
	g.Meta  `path:"/api/v1/admin/commerce/orders/{orderNo}/delivery/revoke" method:"POST" tags:"Admin Commerce" summary:"Revoke active delivery grants"`
	OrderNo string `p:"orderNo" v:"required"`
}

type AdminOrderDeliveryRevokeRes struct {
	Revoked int `json:"revoked"`
}

type AdminOrderDeliveryGrantReq struct {
	g.Meta  `path:"/api/v1/admin/commerce/orders/{orderNo}/delivery/grant" method:"POST" tags:"Admin Commerce" summary:"Create a manual delivery grant"`
	OrderNo string `p:"orderNo" v:"required"`
}

type AdminOrderDeliveryGrantRes struct {
	Token       string `json:"token,omitempty"`
	DeliveryRef string `json:"deliveryRef,omitempty"`
}

type AdminOrderRefundReq struct {
	g.Meta         `path:"/api/v1/admin/commerce/orders/{orderNo}/refund" method:"POST" tags:"Admin Commerce" summary:"Refund an order through its payment provider"`
	OrderNo        string `p:"orderNo" v:"required"`
	AmountCents    int    `json:"amountCents"`
	Reason         string `json:"reason" v:"required"`
	IdempotencyKey string `json:"idempotencyKey" v:"required"`
}

type AdminOrderRefundRes struct {
	RefundNo    string `json:"refundNo"`
	ProviderID  string `json:"providerId,omitempty"`
	Status      string `json:"status"`
	OrderStatus string `json:"orderStatus"`
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
// (Public, no Foundation auth — handled via raw ghttp.Request in the controller,
// not via struct-based binding, because the response must be plaintext "success".)

// ─── POST /dev/orders/{orderNo}/settle ──────────────────────────────────────

// DevSettleReq is the path-param request for dev settle.
type DevSettleReq struct {
	g.Meta  `path:"/dev/orders/{orderNo}/settle" method:"POST" tags:"Dev" summary:"Simulate a successful payment notify (dev only)"`
	OrderNo string `p:"orderNo" v:"required"`
}

// DevSettleRes is the response body for dev settle (empty data).
type DevSettleRes struct {
	Token       string `json:"token,omitempty"`
	DeliveryRef string `json:"deliveryRef,omitempty"`
}
