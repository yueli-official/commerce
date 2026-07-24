package paymentrecovery

import (
	"strings"
	"testing"
)

func TestProviderEventEvidenceIdentity(t *testing.T) {
	observation := paymentObservation(PaymentSettled)
	observation.ProviderTxID = "provider-tx-1"
	observation.PayloadDigest = DigestPayload([]byte("signed provider body"))
	event, err := NewPaymentEvent(observation, "provider-event-1", "TRADE_SUCCESS")
	if err != nil {
		t.Fatal(err)
	}
	if len(event.PayloadDigest) != 64 || strings.Contains(event.PayloadDigest, ":") {
		t.Fatalf("digest = %q", event.PayloadDigest)
	}
	replay := event
	replay.ID = "another-local-id"
	replay.Processing = ProcessingApplied
	if !event.SameEvidence(replay) {
		t.Fatal("processing metadata must not change evidence identity")
	}
	replay.PayloadDigest = DigestPayload([]byte("different body"))
	if event.SameEvidence(replay) {
		t.Fatal("same event key with a different digest must conflict")
	}
}
