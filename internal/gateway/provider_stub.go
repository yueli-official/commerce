package gateway

import (
	"context"
	"fmt"
)

type stubProvider struct {
	provider string
	method   Capability
}

func NewWeChatStubProvider() PaymentGateway {
	return &stubProvider{provider: "wechat", method: CapabilityNativeQR}
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
	case CapabilityNativeQR:
		out.QRCode = fmt.Sprintf("%s://pay/%s", p.provider, in.OrderNo)
	case CapabilityBrowserButton:
		out.ClientToken = "client-" + out.SessionID
	}
	return out, nil
}

func (p *stubProvider) CapturePayment(_ context.Context, in CapturePaymentIn) (*CapturePaymentOut, error) {
	if p.provider != "paypal" {
		return nil, ErrUnsupportedOperation
	}
	return &CapturePaymentOut{
		Success:      true,
		OrderNo:      in.OrderNo,
		ProviderTxID: "CAPTURE-" + in.SessionID,
	}, nil
}

func (p *stubProvider) VerifyNotify(context.Context, []byte, map[string]string) (*NotifyOut, error) {
	return nil, ErrUnsupportedOperation
}

func (p *stubProvider) Refund(context.Context, RefundIn) (*RefundOut, error) {
	return nil, ErrUnsupportedOperation
}
