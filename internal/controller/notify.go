package controller

import (
	"io"
	"strings"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"

	"github.com/yueli-official/commerce/paykit"
	"platform/services/commerce/internal/paymentrecovery"
	"platform/services/commerce/internal/service"
)

const maxProviderNotifyBodyBytes = 1 << 20

// Notify handles POST /api/v1/payments/alipay/notify (public, no Foundation auth).
// The response MUST be the plaintext string "success" or "fail" — Alipay
// requires exactly this; do NOT wrap in the gokit envelope.
type Notify struct {
	provider string
	merchant string
	gw       paykit.Provider
	svc      *service.Service
}

// NewNotify constructs a Notify controller.
func NewNotify(provider string, gw paykit.Provider, svc *service.Service, merchant ...string) *Notify {
	merchantAccount := "primary"
	if len(merchant) > 0 && strings.TrimSpace(merchant[0]) != "" {
		merchantAccount = strings.TrimSpace(merchant[0])
	}
	return &Notify{provider: provider, merchant: merchantAccount, gw: gw, svc: svc}
}

// Handle is a raw ghttp.HandlerFunc — not struct-based — so we bypass the
// envelope and write plaintext directly.
func (c *Notify) Handle(r *ghttp.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProviderNotifyBodyBytes+1))
	if err != nil || len(body) > maxProviderNotifyBodyBytes {
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
	if err != nil || out == nil {
		c.writeFail(r)
		return
	}

	status, ok := recoveryPaymentStatus(out)
	if !ok || strings.TrimSpace(out.OrderNo) == "" {
		c.writeFail(r)
		return
	}
	amountCents := out.AmountCents
	currency := strings.ToUpper(strings.TrimSpace(out.Currency))
	if amountCents <= 0 || currency == "" {
		order, lookupErr := c.svc.GetOrderByNo(r.Context(), out.OrderNo)
		if lookupErr != nil {
			c.writeFail(r)
			return
		}
		if amountCents <= 0 {
			amountCents = order.AmountCents
		}
		if currency == "" {
			currency = strings.ToUpper(strings.TrimSpace(order.Currency))
		}
	}
	occurredAt := out.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	digest := paymentrecovery.DigestPayload(body)
	idempotencyKey := strings.TrimSpace(out.EventID)
	if idempotencyKey == "" {
		idempotencyKey = c.provider + ":payment:" + digest
	}
	_, err = c.svc.AcceptPaymentObservation(
		r.Context(),
		paymentrecovery.PaymentObservation{
			Status: status, Provider: c.provider, Merchant: c.merchant,
			OrderNo: out.OrderNo, ProviderTxID: out.ProviderTxID,
			Money:  paymentrecovery.Money{AmountCents: amountCents, Currency: currency},
			Source: paymentrecovery.SourceCallback, Authoritative: true,
			IdempotencyKey: idempotencyKey, PayloadDigest: digest,
			OccurredAt: occurredAt,
		},
		out.EventID,
		out.ProviderStatus,
	)
	if err != nil {
		c.writeFail(r)
		return
	}

	c.writeSuccess(r)
}

func recoveryPaymentStatus(out *paykit.NotifyOut) (paymentrecovery.PaymentStatus, bool) {
	if out == nil {
		return "", false
	}
	switch out.Status {
	case paykit.PaymentStatusPending:
		return paymentrecovery.PaymentPending, true
	case paykit.PaymentStatusSettled:
		return paymentrecovery.PaymentSettled, true
	case paykit.PaymentStatusFailed:
		return paymentrecovery.PaymentFailed, true
	case paykit.PaymentStatusCancelled:
		return paymentrecovery.PaymentCancelled, true
	case "":
		if out.Success {
			return paymentrecovery.PaymentSettled, true
		}
	}
	return "", false
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
