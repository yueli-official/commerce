package dev_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/yueli-official/commerce/paykit"
	paydev "github.com/yueli-official/commerce/paykit/providers/dev"
)

func TestCreatePaymentReturnsLocalMockCheckout(t *testing.T) {
	provider := paydev.NewProvider()
	out, err := provider.CreatePayment(context.Background(), paykit.CreatePaymentIn{
		OrderNo:     "ORDER-123",
		Subject:     "Design Pack",
		AmountCents: 4900,
		Currency:    "cny",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if out.Provider != "dev" || out.Method != string(paykit.CapabilityRedirect) {
		t.Fatalf("payment contract = %+v", out)
	}
	parsed, err := url.Parse(out.PayURL)
	if err != nil {
		t.Fatalf("parse PayURL: %v", err)
	}
	if parsed.Path != "/checkout/mock-pay" {
		t.Fatalf("PayURL path = %q", parsed.Path)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"orderNo":     "ORDER-123",
		"provider":    "dev",
		"subject":     "Design Pack",
		"amountCents": "4900",
		"currency":    "CNY",
	} {
		if got := query.Get(key); got != want {
			t.Fatalf("PayURL query %s = %q, want %q", key, got, want)
		}
	}
}

func TestRefundSucceedsWithoutExternalProvider(t *testing.T) {
	provider := paydev.NewProvider()
	out, err := provider.Refund(context.Background(), paykit.RefundIn{
		OrderNo:        "ORDER-123",
		RefundNo:       "REFUND-123",
		AmountCents:    4900,
		Currency:       "cny",
		IdempotencyKey: "refund-once",
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if !out.Success || out.Status != paykit.RefundStatusSucceeded ||
		out.ProviderID != "DEV-REFUND-REFUND-123" ||
		out.AmountCents != 4900 || out.Currency != "CNY" ||
		out.ProviderStatus != "succeeded" {
		t.Fatalf("refund contract = %+v", out)
	}
}

func TestUnsupportedProviderOperationsFailClosed(t *testing.T) {
	provider := paydev.NewProvider()
	if _, err := provider.CapturePayment(context.Background(), paykit.CapturePaymentIn{}); !errors.Is(err, paykit.ErrUnsupportedOperation) {
		t.Fatalf("CapturePayment error = %v", err)
	}
	if _, err := provider.VerifyNotify(context.Background(), nil, nil); !errors.Is(err, paykit.ErrUnsupportedOperation) {
		t.Fatalf("VerifyNotify error = %v", err)
	}
}
