// Package paykit defines provider-neutral payment contracts shared by services.
package paykit

import (
	"context"
	"fmt"
)

var ErrUnsupportedOperation = fmt.Errorf("payment provider operation unsupported")

type Capability string

const (
	CapabilityRedirect      Capability = "redirect"
	CapabilityNativeQR      Capability = "native_qr"
	CapabilityBrowserButton Capability = "browser_button"
	CapabilityServerCapture Capability = "server_capture"
	CapabilityRefund        Capability = "refund"
)

type Provider interface {
	Name() string
	CreatePayment(ctx context.Context, in CreatePaymentIn) (*CreatePaymentOut, error)
	CapturePayment(ctx context.Context, in CapturePaymentIn) (*CapturePaymentOut, error)
	VerifyNotify(ctx context.Context, body []byte, headers map[string]string) (*NotifyOut, error)
	Refund(ctx context.Context, in RefundIn) (*RefundOut, error)
}

type QueryPaymentProvider interface {
	QueryPayment(ctx context.Context, in QueryPaymentIn) (*QueryPaymentOut, error)
}

type CreatePaymentIn struct {
	OrderNo     string
	Subject     string
	AmountCents int
	Currency    string
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
	OrderNo     string
	SessionID   string
	AmountCents int
}

type CapturePaymentOut struct {
	Success      bool
	OrderNo      string
	ProviderTxID string
	AmountCents  int
}

type NotifyOut struct {
	Success      bool
	OrderNo      string
	ProviderTxID string
	AmountCents  int
}

type QueryPaymentIn struct {
	OrderNo      string
	ProviderTxID string
	AmountCents  int
}

type QueryPaymentOut struct {
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
