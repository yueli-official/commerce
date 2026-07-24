// Package paykit defines provider-neutral payment contracts shared by services.
package paykit

import (
	"context"
	"fmt"
	"time"
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

// PaymentStatus is the provider-neutral payment fact returned by verified
// callbacks and active queries. A provider adapter must not report settled
// until the provider considers the payment successful.
type PaymentStatus string

const (
	PaymentStatusPending   PaymentStatus = "pending"
	PaymentStatusSettled   PaymentStatus = "settled"
	PaymentStatusFailed    PaymentStatus = "failed"
	PaymentStatusCancelled PaymentStatus = "cancelled"
)

type RefundStatus string

const (
	RefundStatusPending   RefundStatus = "pending"
	RefundStatusSucceeded RefundStatus = "succeeded"
	RefundStatusFailed    RefundStatus = "failed"
	RefundStatusCancelled RefundStatus = "cancelled"
)

// HealthChecker verifies credentials and provider connectivity without
// creating, capturing, refunding, or otherwise mutating a payment.
type HealthChecker interface {
	CheckHealth(ctx context.Context) error
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
	Success        bool
	OrderNo        string
	ProviderTxID   string
	AmountCents    int
	Currency       string
	EventID        string
	Status         PaymentStatus
	ProviderStatus string
	OccurredAt     time.Time
}

type QueryPaymentIn struct {
	OrderNo      string
	SessionID    string
	ProviderTxID string
	AmountCents  int
	Currency     string
}

type QueryPaymentOut struct {
	Success        bool
	OrderNo        string
	ProviderTxID   string
	AmountCents    int
	Currency       string
	ObservationID  string
	Status         PaymentStatus
	ProviderStatus string
	ObservedAt     time.Time
}

type RefundIn struct {
	OrderNo          string
	RefundNo         string
	ProviderTxID     string
	AmountCents      int
	TotalAmountCents int
	Currency         string
	Reason           string
	IdempotencyKey   string
}

type RefundOut struct {
	Success        bool
	ProviderID     string
	AmountCents    int
	Currency       string
	Status         RefundStatus
	ProviderStatus string
}
