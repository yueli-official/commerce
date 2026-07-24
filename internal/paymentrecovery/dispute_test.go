package paymentrecovery

import (
	"errors"
	"testing"
	"time"
)

func TestDisputeAllowsProviderLifecycleRegressionBeforeOutcome(t *testing.T) {
	now := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	current, err := NewDispute(disputeObservation(
		DisputeOpen, "event-open", now,
	))
	if err != nil {
		t.Fatal(err)
	}
	next, changed, err := ApplyDispute(
		current,
		disputeObservation(DisputeUnderReview, "event-review", now.Add(time.Minute)),
	)
	if err != nil || !changed || next.Status != DisputeUnderReview {
		t.Fatalf("under review = %+v changed=%v err=%v", next, changed, err)
	}
	regressed, changed, err := ApplyDispute(
		next,
		disputeObservation(DisputeNeedsResponse, "event-response", now.Add(2*time.Minute)),
	)
	if err != nil || !changed || regressed.Status != DisputeNeedsResponse {
		t.Fatalf("regressed = %+v changed=%v err=%v", regressed, changed, err)
	}
}

func TestDisputeOutcomeIsTerminal(t *testing.T) {
	now := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	current, err := NewDispute(disputeObservation(
		DisputeOpen, "event-open", now,
	))
	if err != nil {
		t.Fatal(err)
	}
	won := disputeObservation(DisputeWon, "event-won", now.Add(time.Minute))
	won.OutcomeCode = "RESOLVED_SELLER_FAVOUR"
	terminal, changed, err := ApplyDispute(current, won)
	if err != nil || !changed || terminal.Status != DisputeWon {
		t.Fatalf("won = %+v changed=%v err=%v", terminal, changed, err)
	}
	late, changed, err := ApplyDispute(
		terminal,
		disputeObservation(DisputeOpen, "event-late", now.Add(2*time.Minute)),
	)
	if err != nil || changed || late.Status != DisputeWon {
		t.Fatalf("late = %+v changed=%v err=%v", late, changed, err)
	}
	_, _, err = ApplyDispute(
		terminal,
		disputeObservation(DisputeLost, "event-conflict", now.Add(3*time.Minute)),
	)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal conflict error = %v", err)
	}
}

func disputeObservation(
	status DisputeStatus,
	key string,
	occurredAt time.Time,
) DisputeObservation {
	return DisputeObservation{
		Status: status, Provider: "paypal", Merchant: "primary",
		OrderNo: "order-1", ProviderTxID: "capture-1",
		ProviderDisputeID: "dispute-1", ProviderStatus: string(status),
		Money:  Money{AmountCents: 900, Currency: "USD"},
		Source: SourceCallback, Authoritative: true,
		IdempotencyKey: key, PayloadDigest: DigestPayload([]byte(key)),
		OccurredAt: occurredAt,
	}
}
