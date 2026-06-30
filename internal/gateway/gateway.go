// Package gateway defines the PaymentGateway abstraction consumed by
// the commerce service HTTP handlers (Task 4) and implemented by
// provider packages (e.g. alipay).
package gateway

import "context"

type Capability string

const (
	CapabilityRedirect      Capability = "redirect"
	CapabilityNativeQR      Capability = "native_qr"
	CapabilityBrowserButton Capability = "browser_button"
	CapabilityServerCapture Capability = "server_capture"
	CapabilityRefund        Capability = "refund"
)

// PaymentGateway is the interface all payment providers must satisfy.
type PaymentGateway interface {
	// CreatePayment creates a new payment session and returns a redirect URL
	// that the buyer should be sent to in order to complete payment.
	CreatePayment(ctx context.Context, in CreateIn) (*CreatePaymentOut, error)

	CapturePayment(ctx context.Context, in CapturePaymentIn) (*CapturePaymentOut, error)

	// VerifyNotify authenticates an incoming async notify callback from the
	// payment provider, verifying the signature and extracting the result.
	// Returns an error when the signature is invalid or the body is malformed.
	VerifyNotify(ctx context.Context, body []byte, headers map[string]string) (*NotifyOut, error)

	Refund(ctx context.Context, in RefundIn) (*RefundOut, error)
}

// CreateIn carries the parameters needed to initiate a payment.
type CreateIn struct {
	OrderNo     string
	Subject     string
	AmountCents int
	NotifyURL   string
	ReturnURL   string
}

type CreatePaymentOut struct {
	Provider    string
	Method      string
	PayURL      string
	SessionID   string
	QRCode      string
	ClientToken string
}

type CapturePaymentIn struct {
	OrderNo   string
	SessionID string
}

type CapturePaymentOut struct {
	Success      bool
	OrderNo      string
	ProviderTxID string
	AmountCents  int
}

// NotifyOut carries the result of a verified async payment notification.
type NotifyOut struct {
	Success      bool
	OrderNo      string
	ProviderTxID string
	AmountCents  int
}

type RefundIn struct {
	OrderNo      string
	ProviderTxID string
	AmountCents  int
	Reason       string
}

type RefundOut struct {
	Success     bool
	ProviderID  string
	AmountCents int
}

// Registry maps provider slugs to their gateway implementations.
type Registry map[string]PaymentGateway
