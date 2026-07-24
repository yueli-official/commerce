package paymentrecovery

import (
	"errors"
	"testing"
	"time"
)

var observedAt = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

func TestPaymentConvergesAcrossPendingSettlementAndDuplicate(t *testing.T) {
	payment := mustPayment(t)
	pending := paymentObservation(PaymentPending)
	next, changed, err := ApplyPayment(payment, pending)
	if err != nil || !changed || next.Status != PaymentPending {
		t.Fatalf("pending = %+v changed=%v err=%v", next, changed, err)
	}
	settled := paymentObservation(PaymentSettled)
	settled.ProviderTxID = "provider-tx-1"
	next, changed, err = ApplyPayment(next, settled)
	if err != nil || !changed || next.Status != PaymentSettled || next.ProviderTxID != "provider-tx-1" {
		t.Fatalf("settled = %+v changed=%v err=%v", next, changed, err)
	}
	replay, changed, err := ApplyPayment(next, settled)
	if err != nil || changed || replay != next {
		t.Fatalf("settled replay = %+v changed=%v err=%v", replay, changed, err)
	}
	stale, changed, err := ApplyPayment(next, pending)
	if err != nil || changed || stale != next {
		t.Fatalf("stale pending = %+v changed=%v err=%v", stale, changed, err)
	}
}

func TestPaymentRejectsConflictingBindingsAndTerminalRewrite(t *testing.T) {
	payment := mustPayment(t)
	settled := paymentObservation(PaymentSettled)
	settled.ProviderTxID = "provider-tx-1"
	payment, _, _ = ApplyPayment(payment, settled)

	conflict := settled
	conflict.ProviderTxID = "provider-tx-2"
	if _, _, err := ApplyPayment(payment, conflict); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("provider transaction conflict = %v", err)
	}
	failed := paymentObservation(PaymentFailed)
	if _, _, err := ApplyPayment(payment, failed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal rewrite = %v", err)
	}
}

func TestPaymentRequiresAuthoritativeEvidence(t *testing.T) {
	payment := mustPayment(t)
	observation := paymentObservation(PaymentPending)
	observation.Authoritative = false
	if _, _, err := ApplyPayment(payment, observation); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("unauthenticated observation = %v", err)
	}
}

func TestRefundKeepsPendingDistinctFromSucceeded(t *testing.T) {
	refund, err := NewRefund("paypal", "primary", "order-1", "refund-1", Money{AmountCents: 500, Currency: "usd"})
	if err != nil {
		t.Fatal(err)
	}
	pending := refundObservation(RefundPending)
	next, changed, err := ApplyRefund(refund, pending)
	if err != nil || !changed || next.Status != RefundPending {
		t.Fatalf("pending = %+v changed=%v err=%v", next, changed, err)
	}
	succeeded := refundObservation(RefundSucceeded)
	succeeded.ProviderRefundID = "provider-refund-1"
	next, changed, err = ApplyRefund(next, succeeded)
	if err != nil || !changed || next.Status != RefundSucceeded {
		t.Fatalf("succeeded = %+v changed=%v err=%v", next, changed, err)
	}
	stale, changed, err := ApplyRefund(next, pending)
	if err != nil || changed || stale != next {
		t.Fatalf("stale pending = %+v changed=%v err=%v", stale, changed, err)
	}
}

func TestRefundSuccessRequiresProviderIdentity(t *testing.T) {
	refund, err := NewRefund("paypal", "primary", "order-1", "refund-1", Money{AmountCents: 500, Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyRefund(refund, refundObservation(RefundSucceeded)); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("refund without provider id = %v", err)
	}
}

func mustPayment(t *testing.T) Payment {
	t.Helper()
	payment, err := NewPayment("alipay", "primary", "order-1", Money{AmountCents: 1000, Currency: "cny"})
	if err != nil {
		t.Fatal(err)
	}
	return payment
}

func paymentObservation(status PaymentStatus) PaymentObservation {
	return PaymentObservation{
		Status: status, Provider: "alipay", Merchant: "primary", OrderNo: "order-1",
		Money: Money{AmountCents: 1000, Currency: "CNY"}, Source: SourceQuery,
		Authoritative: true, IdempotencyKey: "payment-observation-1",
		PayloadDigest: "sha256:payment", OccurredAt: observedAt,
	}
}

func refundObservation(status RefundStatus) RefundObservation {
	return RefundObservation{
		Status: status, Provider: "paypal", Merchant: "primary", OrderNo: "order-1",
		RefundNo: "refund-1", Money: Money{AmountCents: 500, Currency: "USD"},
		Source: SourceQuery, Authoritative: true, IdempotencyKey: "refund-observation-1",
		PayloadDigest: "sha256:refund", OccurredAt: observedAt,
	}
}
