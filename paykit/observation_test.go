package paykit_test

import (
	"errors"
	"testing"
	"time"

	"github.com/yueli-official/commerce/paykit"
)

func TestPaymentQueryConformance(t *testing.T) {
	now := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	input := paykit.QueryPaymentIn{OrderNo: "ORDER-1", AmountCents: 1200, Currency: "cny"}
	for _, test := range []struct {
		name    string
		out     paykit.QueryPaymentOut
		wantErr bool
	}{
		{name: "pending", out: paykit.QueryPaymentOut{
			OrderNo: "ORDER-1", AmountCents: 1200, Currency: "CNY",
			Status: paykit.PaymentStatusPending, ProviderStatus: "WAITING",
		}},
		{name: "settled", out: paykit.QueryPaymentOut{
			Success: true, OrderNo: "ORDER-1", ProviderTxID: "TX-1",
			AmountCents: 1200, Currency: "CNY",
			Status: paykit.PaymentStatusSettled, ProviderStatus: "SUCCESS",
		}},
		{name: "wrong order", out: paykit.QueryPaymentOut{
			OrderNo: "ORDER-2", AmountCents: 1200, Currency: "CNY",
			Status: paykit.PaymentStatusPending, ProviderStatus: "WAITING",
		}, wantErr: true},
		{name: "wrong money", out: paykit.QueryPaymentOut{
			OrderNo: "ORDER-1", AmountCents: 100, Currency: "USD",
			Status: paykit.PaymentStatusPending, ProviderStatus: "WAITING",
		}, wantErr: true},
		{name: "settled without transaction", out: paykit.QueryPaymentOut{
			Success: true, OrderNo: "ORDER-1", AmountCents: 1200, Currency: "CNY",
			Status: paykit.PaymentStatusSettled, ProviderStatus: "SUCCESS",
		}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := paykit.NormalizePaymentQuery(input, &test.out, now)
			if test.wantErr {
				if !errors.Is(err, paykit.ErrInvalidObservation) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !out.ObservedAt.Equal(now) || out.Currency != "CNY" {
				t.Fatalf("normalized = %+v", out)
			}
		})
	}
}

func TestRefundQueryConformance(t *testing.T) {
	now := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	input := paykit.QueryRefundIn{
		RefundNo: "REFUND-1", ProviderRefundID: "PROVIDER-1",
		AmountCents: 600, Currency: "CNY",
	}
	for _, status := range []paykit.RefundStatus{
		paykit.RefundStatusPending, paykit.RefundStatusFailed,
		paykit.RefundStatusCancelled, paykit.RefundStatusSucceeded,
	} {
		out := paykit.QueryRefundOut{
			ProviderID: "PROVIDER-1", AmountCents: 600, Currency: "cny",
			Status: status, ProviderStatus: string(status),
			Success: status == paykit.RefundStatusSucceeded,
		}
		normalized, err := paykit.NormalizeRefundQuery(input, &out, now)
		if err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if !normalized.ObservedAt.Equal(now) {
			t.Fatalf("%s observedAt = %s", status, normalized.ObservedAt)
		}
	}
	bad := paykit.QueryRefundOut{
		ProviderID: "OTHER", AmountCents: 600, Currency: "CNY",
		Status: paykit.RefundStatusPending, ProviderStatus: "PENDING",
	}
	if _, err := paykit.NormalizeRefundQuery(input, &bad, now); !errors.Is(err, paykit.ErrInvalidObservation) {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestRefundMutationConformance(t *testing.T) {
	input := paykit.RefundIn{AmountCents: 600, Currency: "USD"}
	valid := paykit.RefundOut{
		Success: true, ProviderID: "PROVIDER-1", AmountCents: 600,
		Currency: "usd", Status: paykit.RefundStatusSucceeded,
		ProviderStatus: "COMPLETED",
	}
	if _, err := paykit.NormalizeRefundResult(input, &valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.AmountCents = 601
	if _, err := paykit.NormalizeRefundResult(input, &invalid); !errors.Is(err, paykit.ErrInvalidObservation) {
		t.Fatalf("mismatch error = %v", err)
	}
}
