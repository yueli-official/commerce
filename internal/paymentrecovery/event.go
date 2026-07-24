package paymentrecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var ErrEventConflict = errors.New("commerce payment recovery: provider event conflict")

type Operation string

const (
	OperationPayment Operation = "payment"
	OperationRefund  Operation = "refund"
	OperationDispute Operation = "dispute"
)

type ProcessingState string

const (
	ProcessingReceived ProcessingState = "received"
	ProcessingApplied  ProcessingState = "applied"
	ProcessingIgnored  ProcessingState = "ignored"
	ProcessingConflict ProcessingState = "conflict"
	ProcessingFailed   ProcessingState = "failed"
)

// ProviderEvent is the immutable evidence envelope persisted before a provider
// observation can change transaction state. Only processing fields are mutable.
type ProviderEvent struct {
	ID               string          `orm:"id"`
	Provider         string          `orm:"provider"`
	Merchant         string          `orm:"merchant_account"`
	Source           Source          `orm:"source"`
	Operation        Operation       `orm:"operation"`
	IdempotencyKey   string          `orm:"idempotency_key"`
	ProviderEventID  string          `orm:"provider_event_id"`
	PayloadDigest    string          `orm:"payload_digest"`
	ProviderStatus   string          `orm:"provider_status"`
	NormalizedStatus string          `orm:"normalized_status"`
	OrderNo          string          `orm:"order_no"`
	ProviderObjectID string          `orm:"provider_object_id"`
	OrderID          string          `orm:"order_id"`
	PaymentAttemptID string          `orm:"payment_attempt_id"`
	RefundID         string          `orm:"refund_id"`
	DisputeID        string          `orm:"dispute_id"`
	AmountCents      int             `orm:"amount_cents"`
	Currency         string          `orm:"currency"`
	OccurredAt       *time.Time      `orm:"occurred_at"`
	Processing       ProcessingState `orm:"processing_state"`
	ProcessingError  string          `orm:"processing_error"`
	ProcessedAt      *time.Time      `orm:"processed_at"`
	ReceivedAt       time.Time       `orm:"received_at"`
}

type PaymentAttemptRecord struct {
	ID                string        `orm:"id"`
	OrderID           string        `orm:"order_id"`
	Provider          string        `orm:"provider"`
	Merchant          string        `orm:"merchant_account"`
	IdempotencyKey    string        `orm:"idempotency_key"`
	Status            PaymentStatus `orm:"status"`
	AmountCents       int           `orm:"amount_cents"`
	Currency          string        `orm:"currency"`
	ProviderSessionID string        `orm:"provider_session_id"`
	ProviderTxID      string        `orm:"provider_tx_id"`
	Revision          uint64        `orm:"revision"`
	LastObservedAt    *time.Time    `orm:"last_observed_at"`
	CreatedAt         time.Time     `orm:"created_at"`
	UpdatedAt         time.Time     `orm:"updated_at"`
}

type DuePaymentAttempt struct {
	ID              string    `orm:"id"`
	OrderNo         string    `orm:"order_no"`
	Provider        string    `orm:"provider"`
	NextReconcileAt time.Time `orm:"next_reconcile_at"`
}

func DigestPayload(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func NewPaymentEvent(observation PaymentObservation, providerEventID, providerStatus string) (ProviderEvent, error) {
	observation = normalizePaymentObservation(observation)
	if err := validatePaymentObservation(observation); err != nil {
		return ProviderEvent{}, err
	}
	event := ProviderEvent{
		Provider: observation.Provider, Merchant: observation.Merchant,
		Source: observation.Source, Operation: OperationPayment,
		IdempotencyKey:   observation.IdempotencyKey,
		ProviderEventID:  strings.TrimSpace(providerEventID),
		PayloadDigest:    observation.PayloadDigest,
		ProviderStatus:   strings.TrimSpace(providerStatus),
		NormalizedStatus: string(observation.Status),
		OrderNo:          observation.OrderNo, ProviderObjectID: observation.ProviderTxID,
		AmountCents: observation.Money.AmountCents, Currency: observation.Money.Currency,
		OccurredAt: &observation.OccurredAt, Processing: ProcessingReceived,
	}
	if event.ProviderStatus == "" {
		event.ProviderStatus = event.NormalizedStatus
	}
	return event, nil
}

// SameEvidence checks the immutable identity of a replay. Processing metadata
// and database links are deliberately excluded.
func (event ProviderEvent) SameEvidence(other ProviderEvent) bool {
	return event.Provider == other.Provider &&
		event.Merchant == other.Merchant &&
		event.Source == other.Source &&
		event.Operation == other.Operation &&
		event.IdempotencyKey == other.IdempotencyKey &&
		event.ProviderEventID == other.ProviderEventID &&
		event.PayloadDigest == other.PayloadDigest &&
		event.ProviderStatus == other.ProviderStatus &&
		event.NormalizedStatus == other.NormalizedStatus &&
		event.OrderNo == other.OrderNo &&
		event.ProviderObjectID == other.ProviderObjectID &&
		event.AmountCents == other.AmountCents &&
		event.Currency == other.Currency
}
