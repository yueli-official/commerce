// Package dev provides an explicit local-only payment adapter.
//
// It deliberately uses the provider name "dev" so local simulation can never
// be mistaken for a real Alipay, WeChat Pay, or PayPal transaction.
package dev

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"platform/paykit"
)

type provider struct{}

var (
	_ paykit.Provider             = (*provider)(nil)
	_ paykit.QueryPaymentProvider = (*provider)(nil)
	_ paykit.QueryRefundProvider  = (*provider)(nil)
	_ paykit.HealthChecker        = (*provider)(nil)
)

func NewProvider() paykit.Provider {
	return &provider{}
}

func (*provider) Name() string {
	return "dev"
}

func (*provider) CheckHealth(context.Context) error {
	return nil
}

func (*provider) CreatePayment(_ context.Context, in paykit.CreatePaymentIn) (*paykit.CreatePaymentOut, error) {
	orderNo := strings.TrimSpace(in.OrderNo)
	if orderNo == "" {
		return nil, fmt.Errorf("dev payment order number is required")
	}
	if in.AmountCents <= 0 {
		return nil, fmt.Errorf("dev payment amount must be positive")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		return nil, fmt.Errorf("dev payment currency is required")
	}
	query := url.Values{
		"amountCents": {strconv.Itoa(in.AmountCents)},
		"currency":    {currency},
		"orderNo":     {orderNo},
		"provider":    {"dev"},
		"subject":     {strings.TrimSpace(in.Subject)},
	}
	return &paykit.CreatePaymentOut{
		Provider: "dev",
		Method:   string(paykit.CapabilityRedirect),
		PayURL:   "/checkout/mock-pay?" + query.Encode(),
	}, nil
}

func (*provider) CapturePayment(context.Context, paykit.CapturePaymentIn) (*paykit.CapturePaymentOut, error) {
	return nil, paykit.ErrUnsupportedOperation
}

func (*provider) VerifyNotify(context.Context, []byte, map[string]string) (*paykit.NotifyOut, error) {
	return nil, paykit.ErrUnsupportedOperation
}

func (*provider) Refund(_ context.Context, in paykit.RefundIn) (*paykit.RefundOut, error) {
	refundNo := strings.TrimSpace(in.RefundNo)
	if refundNo == "" {
		return nil, fmt.Errorf("dev refund number is required")
	}
	if in.AmountCents <= 0 {
		return nil, fmt.Errorf("dev refund amount must be positive")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		return nil, fmt.Errorf("dev refund currency is required")
	}
	return &paykit.RefundOut{
		Success:        true,
		ProviderID:     "DEV-REFUND-" + refundNo,
		AmountCents:    in.AmountCents,
		Currency:       currency,
		Status:         paykit.RefundStatusSucceeded,
		ProviderStatus: "succeeded",
	}, nil
}

func (*provider) QueryPayment(_ context.Context, in paykit.QueryPaymentIn) (*paykit.QueryPaymentOut, error) {
	return &paykit.QueryPaymentOut{
		OrderNo:        strings.TrimSpace(in.OrderNo),
		ProviderTxID:   strings.TrimSpace(in.ProviderTxID),
		AmountCents:    in.AmountCents,
		Currency:       strings.ToUpper(strings.TrimSpace(in.Currency)),
		ObservationID:  "dev-payment-query-" + strings.TrimSpace(in.OrderNo),
		Status:         paykit.PaymentStatusPending,
		ProviderStatus: "pending_local_confirmation",
	}, nil
}

func (*provider) QueryRefund(_ context.Context, in paykit.QueryRefundIn) (*paykit.QueryRefundOut, error) {
	refundNo := strings.TrimSpace(in.RefundNo)
	return &paykit.QueryRefundOut{
		Success:        true,
		ProviderID:     firstNonEmpty(strings.TrimSpace(in.ProviderRefundID), "DEV-REFUND-"+refundNo),
		AmountCents:    in.AmountCents,
		Currency:       strings.ToUpper(strings.TrimSpace(in.Currency)),
		ObservationID:  "dev-refund-query-" + refundNo,
		Status:         paykit.RefundStatusSucceeded,
		ProviderStatus: "succeeded",
	}, nil
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
