package paykit

import (
	"context"
	"testing"
)

func TestRegistryRegisterNormalizesProviderNames(t *testing.T) {
	reg := NewRegistry()
	provider := NewFakeProvider(" PayPal ")

	if err := reg.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := reg.Get("paypal")
	if !ok {
		t.Fatal("provider was not found by normalized name")
	}
	if got != provider {
		t.Fatal("registry returned a different provider")
	}
}

func TestRegistryRejectsDuplicateProviderNames(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(NewFakeProvider("paypal")); err != nil {
		t.Fatalf("Register first provider: %v", err)
	}
	if err := reg.Register(NewFakeProvider(" PayPal ")); err == nil {
		t.Fatal("duplicate provider name was accepted")
	}
}

func TestFakeProviderCreateCaptureNotifyRefund(t *testing.T) {
	provider := NewFakeProvider("fake")
	provider.NextCreate = CreatePaymentOut{
		Provider:  "fake",
		Method:    string(CapabilityRedirect),
		PayURL:    "https://pay.example.test/order",
		SessionID: "session-1",
	}
	provider.NextCapture = CapturePaymentOut{
		Success:      true,
		OrderNo:      "ORD-1",
		ProviderTxID: "CAP-1",
		AmountCents:  9900,
	}
	provider.NextNotify = NotifyOut{
		Success:      true,
		OrderNo:      "ORD-1",
		ProviderTxID: "TX-1",
		AmountCents:  9900,
	}
	provider.NextRefund = RefundOut{
		Success:     true,
		ProviderID:  "RF-1",
		AmountCents: 9900,
	}

	create, err := provider.CreatePayment(context.Background(), CreatePaymentIn{
		OrderNo:     "ORD-1",
		Subject:     "Asset",
		AmountCents: 9900,
		Currency:    "USD",
		NotifyURL:   "https://commerce.example.test/notify",
		ReturnURL:   "https://shop.example.test/return",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if create.SessionID != "session-1" || create.PayURL == "" {
		t.Fatalf("unexpected create output: %+v", create)
	}

	capture, err := provider.CapturePayment(context.Background(), CapturePaymentIn{
		OrderNo:     "ORD-1",
		SessionID:   "session-1",
		AmountCents: 9900,
	})
	if err != nil {
		t.Fatalf("CapturePayment: %v", err)
	}
	if capture.ProviderTxID != "CAP-1" {
		t.Fatalf("unexpected capture output: %+v", capture)
	}

	notify, err := provider.VerifyNotify(context.Background(), []byte("signed-body"), map[string]string{"X-Test": "1"})
	if err != nil {
		t.Fatalf("VerifyNotify: %v", err)
	}
	if notify.ProviderTxID != "TX-1" {
		t.Fatalf("unexpected notify output: %+v", notify)
	}

	refund, err := provider.Refund(context.Background(), RefundIn{
		OrderNo:      "ORD-1",
		ProviderTxID: "CAP-1",
		AmountCents:  9900,
		Reason:       "customer request",
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if refund.ProviderID != "RF-1" {
		t.Fatalf("unexpected refund output: %+v", refund)
	}

	if len(provider.CreateCalls) != 1 || provider.CreateCalls[0].OrderNo != "ORD-1" {
		t.Fatalf("create calls not recorded: %+v", provider.CreateCalls)
	}
	if string(provider.NotifyCalls[0].Body) != "signed-body" {
		t.Fatalf("notify call body not recorded: %+v", provider.NotifyCalls)
	}
}
