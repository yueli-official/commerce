package paymentrecovery

import (
	"fmt"
	"strings"
	"time"
)

type DisputeStatus string

const (
	DisputeOpen          DisputeStatus = "open"
	DisputeNeedsResponse DisputeStatus = "needs_response"
	DisputeUnderReview   DisputeStatus = "under_review"
	DisputeWon           DisputeStatus = "won"
	DisputeLost          DisputeStatus = "lost"
	DisputeAccepted      DisputeStatus = "accepted"
	DisputeClosed        DisputeStatus = "closed"
)

type Dispute struct {
	Status            DisputeStatus
	Provider          string
	Merchant          string
	OrderNo           string
	ProviderTxID      string
	ProviderDisputeID string
	ProviderStatus    string
	OutcomeCode       string
	Money             Money
	ReasonCode        string
	OpenedAt          time.Time
	DueAt             time.Time
	Revision          uint64
	LastObservedAt    time.Time
}

type DisputeObservation struct {
	Status            DisputeStatus
	Provider          string
	Merchant          string
	OrderNo           string
	ProviderTxID      string
	ProviderDisputeID string
	ProviderStatus    string
	OutcomeCode       string
	Money             Money
	ReasonCode        string
	OpenedAt          time.Time
	DueAt             time.Time
	Source            Source
	Authoritative     bool
	IdempotencyKey    string
	PayloadDigest     string
	OccurredAt        time.Time
}

func NewDispute(observation DisputeObservation) (Dispute, error) {
	observation = normalizeDisputeObservation(observation)
	if err := validateDisputeObservation(observation); err != nil {
		return Dispute{}, err
	}
	return Dispute{
		Status: observation.Status, Provider: observation.Provider,
		Merchant: observation.Merchant, OrderNo: observation.OrderNo,
		ProviderTxID:      observation.ProviderTxID,
		ProviderDisputeID: observation.ProviderDisputeID,
		ProviderStatus:    observation.ProviderStatus,
		OutcomeCode:       observation.OutcomeCode, Money: observation.Money,
		ReasonCode: observation.ReasonCode, OpenedAt: observation.OpenedAt,
		DueAt: observation.DueAt, Revision: 1,
		LastObservedAt: observation.OccurredAt,
	}, nil
}

func ApplyDispute(
	current Dispute,
	observation DisputeObservation,
) (Dispute, bool, error) {
	if err := validateDispute(current); err != nil {
		return Dispute{}, false, err
	}
	observation = normalizeDisputeObservation(observation)
	if err := validateDisputeObservation(observation); err != nil {
		return Dispute{}, false, err
	}
	if current.Provider != observation.Provider ||
		current.Merchant != observation.Merchant ||
		current.OrderNo != observation.OrderNo ||
		current.ProviderTxID != observation.ProviderTxID ||
		current.ProviderDisputeID != observation.ProviderDisputeID ||
		current.Money != observation.Money {
		return Dispute{}, false, ErrBindingConflict
	}
	if disputeTerminal(current.Status) {
		if current.Status != observation.Status && disputeTerminal(observation.Status) {
			return Dispute{}, false, fmt.Errorf(
				"%w: dispute %s cannot become %s",
				ErrInvalidTransition, current.Status, observation.Status,
			)
		}
		return current, false, nil
	}
	if sameDisputeObservation(current, observation) {
		return current, false, nil
	}
	next := current
	next.Status = observation.Status
	next.ProviderStatus = observation.ProviderStatus
	next.OutcomeCode = observation.OutcomeCode
	next.ReasonCode = observation.ReasonCode
	if !observation.OpenedAt.IsZero() {
		next.OpenedAt = observation.OpenedAt
	}
	next.DueAt = observation.DueAt
	next.LastObservedAt = observation.OccurredAt
	next.Revision++
	return next, true, nil
}

func validateDispute(dispute Dispute) error {
	if dispute.Provider == "" || dispute.Merchant == "" ||
		dispute.OrderNo == "" || dispute.ProviderTxID == "" ||
		dispute.ProviderDisputeID == "" || dispute.Revision == 0 ||
		!validMoney(dispute.Money) || !validDisputeStatus(dispute.Status) {
		return ErrInvalidEvidence
	}
	return nil
}

func validateDisputeObservation(observation DisputeObservation) error {
	if !observation.Authoritative || observation.IdempotencyKey == "" ||
		observation.PayloadDigest == "" || observation.OccurredAt.IsZero() ||
		observation.Provider == "" || observation.Merchant == "" ||
		observation.ProviderTxID == "" ||
		observation.ProviderDisputeID == "" ||
		!validMoney(observation.Money) ||
		!validDisputeStatus(observation.Status) {
		return ErrInvalidEvidence
	}
	switch observation.Source {
	case SourceCallback, SourceQuery:
		return nil
	default:
		return ErrInvalidEvidence
	}
}

func validDisputeStatus(status DisputeStatus) bool {
	switch status {
	case DisputeOpen, DisputeNeedsResponse, DisputeUnderReview,
		DisputeWon, DisputeLost, DisputeAccepted, DisputeClosed:
		return true
	default:
		return false
	}
}

func disputeTerminal(status DisputeStatus) bool {
	switch status {
	case DisputeWon, DisputeLost, DisputeAccepted, DisputeClosed:
		return true
	default:
		return false
	}
}

func sameDisputeObservation(
	current Dispute,
	observation DisputeObservation,
) bool {
	return current.Status == observation.Status &&
		current.ProviderStatus == observation.ProviderStatus &&
		current.OutcomeCode == observation.OutcomeCode &&
		current.ReasonCode == observation.ReasonCode &&
		current.OpenedAt.Equal(observation.OpenedAt) &&
		current.DueAt.Equal(observation.DueAt)
}

func normalizeDisputeObservation(
	observation DisputeObservation,
) DisputeObservation {
	observation.Provider = strings.TrimSpace(observation.Provider)
	observation.Merchant = strings.TrimSpace(observation.Merchant)
	observation.OrderNo = strings.TrimSpace(observation.OrderNo)
	observation.ProviderTxID = strings.TrimSpace(observation.ProviderTxID)
	observation.ProviderDisputeID = strings.TrimSpace(observation.ProviderDisputeID)
	observation.ProviderStatus = strings.TrimSpace(observation.ProviderStatus)
	observation.OutcomeCode = strings.TrimSpace(observation.OutcomeCode)
	observation.ReasonCode = strings.TrimSpace(observation.ReasonCode)
	observation.IdempotencyKey = strings.TrimSpace(observation.IdempotencyKey)
	observation.PayloadDigest = strings.TrimSpace(observation.PayloadDigest)
	observation.Money = normalizeMoney(observation.Money)
	observation.OpenedAt = observation.OpenedAt.UTC()
	observation.DueAt = observation.DueAt.UTC()
	observation.OccurredAt = observation.OccurredAt.UTC()
	return observation
}
