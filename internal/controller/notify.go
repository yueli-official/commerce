package controller

import (
	"io"

	"github.com/gogf/gf/v2/net/ghttp"

	"platform/paykit"
	"platform/services/commerce/internal/service"
)

// Notify handles POST /api/v1/payments/alipay/notify (public, no authjwt).
// The response MUST be the plaintext string "success" or "fail" — Alipay
// requires exactly this; do NOT wrap in the gokit envelope.
type Notify struct {
	provider string
	gw       paykit.Provider
	svc      *service.Service
}

// NewNotify constructs a Notify controller.
func NewNotify(provider string, gw paykit.Provider, svc *service.Service) *Notify {
	return &Notify{provider: provider, gw: gw, svc: svc}
}

// Handle is a raw ghttp.HandlerFunc — not struct-based — so we bypass the
// envelope and write plaintext directly.
func (c *Notify) Handle(r *ghttp.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		c.writeFail(r)
		return
	}

	// headers is reserved for future providers (e.g. WeChat Pay / PayPal) that
	// carry their signatures in HTTP headers rather than the request body.
	headers := make(map[string]string)
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}

	out, err := c.gw.VerifyNotify(r.Context(), body, headers)
	if err != nil || out == nil || !out.Success {
		c.writeFail(r)
		return
	}

	if _, err := c.svc.SettleCheckout(r.Context(), out.OrderNo, c.provider, out.ProviderTxID, out.AmountCents); err != nil {
		c.writeFail(r)
		return
	}

	c.writeSuccess(r)
}

func (c *Notify) writeSuccess(r *ghttp.Request) {
	if c.provider == "wechat" {
		r.Response.WriteJson(map[string]string{"code": "SUCCESS", "message": "成功"})
		return
	}
	r.Response.Write("success")
}

func (c *Notify) writeFail(r *ghttp.Request) {
	if c.provider == "wechat" {
		r.Response.Status = 500
		r.Response.WriteJson(map[string]string{"code": "FAIL", "message": "失败"})
		return
	}
	r.Response.Write("fail")
}
