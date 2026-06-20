package controller

import (
	"io"

	"github.com/gogf/gf/v2/net/ghttp"

	"platform/services/commerce/internal/gateway"
	"platform/services/commerce/internal/service"
)

// Notify handles POST /api/v1/payments/alipay/notify (public, no authjwt).
// The response MUST be the plaintext string "success" or "fail" — Alipay
// requires exactly this; do NOT wrap in the gokit envelope.
type Notify struct {
	gw  gateway.PaymentGateway
	svc *service.Service
}

// NewNotify constructs a Notify controller.
func NewNotify(gw gateway.PaymentGateway, svc *service.Service) *Notify {
	return &Notify{gw: gw, svc: svc}
}

// Handle is a raw ghttp.HandlerFunc — not struct-based — so we bypass the
// envelope and write plaintext directly.
func (c *Notify) Handle(r *ghttp.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		r.Response.Write("fail")
		return
	}

	headers := make(map[string]string)
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}

	out, err := c.gw.VerifyNotify(r.Context(), body, headers)
	if err != nil || out == nil || !out.Success {
		r.Response.Write("fail")
		return
	}

	if err := c.svc.MarkPaid(r.Context(), out.OrderNo, out.ProviderTxID, out.AmountCents); err != nil {
		r.Response.Write("fail")
		return
	}

	r.Response.Write("success")
}
