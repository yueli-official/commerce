package gateway

import (
	"context"
	"fmt"
	"net/url"
)

type stubProvider struct {
	provider string
	method   Capability
}

func NewWeChatStubProvider() PaymentGateway {
	return &stubProvider{provider: "wechat", method: CapabilityNativeQR}
}

func NewAlipayStubProvider() PaymentGateway {
	return &stubProvider{provider: "alipay", method: CapabilityRedirect}
}

func NewPayPalStubProvider() PaymentGateway {
	return &stubProvider{provider: "paypal", method: CapabilityBrowserButton}
}

func (p *stubProvider) CreatePayment(_ context.Context, in CreateIn) (*CreatePaymentOut, error) {
	out := &CreatePaymentOut{
		Provider:  p.provider,
		Method:    string(p.method),
		SessionID: p.provider + "-" + in.OrderNo,
	}
	switch p.method {
	case CapabilityRedirect:
		out.PayURL = mockPaymentURL(in, p.provider)
	case CapabilityNativeQR:
		out.QRCode = mockPaymentURL(in, p.provider)
	case CapabilityBrowserButton:
		out.ClientToken = "client-" + out.SessionID
	}
	return out, nil
}

func mockPaymentURL(in CreateIn, provider string) string {
	if in.ReturnURL == "" {
		return fmt.Sprintf("%s://pay/%s", provider, in.OrderNo)
	}
	u, err := url.Parse(in.ReturnURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Sprintf("%s://pay/%s", provider, in.OrderNo)
	}
	mock := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/checkout/mock-pay"}
	q := mock.Query()
	q.Set("orderNo", in.OrderNo)
	q.Set("provider", provider)
	q.Set("amountCents", fmt.Sprintf("%d", in.AmountCents))
	q.Set("currency", in.Currency)
	if in.Subject != "" {
		q.Set("subject", in.Subject)
	}
	mock.RawQuery = q.Encode()
	return mock.String()
}

func (p *stubProvider) CapturePayment(_ context.Context, in CapturePaymentIn) (*CapturePaymentOut, error) {
	if p.provider != "paypal" {
		return nil, ErrUnsupportedOperation
	}
	return &CapturePaymentOut{
		Success:      true,
		OrderNo:      in.OrderNo,
		ProviderTxID: "CAPTURE-" + in.SessionID,
		AmountCents:  in.AmountCents,
	}, nil
}

func (p *stubProvider) VerifyNotify(context.Context, []byte, map[string]string) (*NotifyOut, error) {
	return nil, ErrUnsupportedOperation
}

func (p *stubProvider) Refund(context.Context, RefundIn) (*RefundOut, error) {
	return nil, ErrUnsupportedOperation
}
