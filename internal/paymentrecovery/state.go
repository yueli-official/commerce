package paymentrecovery

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidEvidence   = errors.New("commerce payment recovery: invalid evidence")
	ErrInvalidTransition = errors.New("commerce payment recovery: invalid transition")
	ErrBindingConflict   = errors.New("commerce payment recovery: provider binding conflict")
)

type PaymentStatus string

const (
	PaymentCreated        PaymentStatus = "created"
	PaymentActionRequired PaymentStatus = "action_required"
	PaymentPending        PaymentStatus = "pending"
	PaymentSettled        PaymentStatus = "settled"
	PaymentFailed         PaymentStatus = "failed"
	PaymentCancelled      PaymentStatus = "cancelled"
)

type RefundStatus string

const (
	RefundRequested  RefundStatus = "requested"
	RefundSubmitting RefundStatus = "submitting"
	RefundPending    RefundStatus = "pending"
	RefundSucceeded  RefundStatus = "succeeded"
	RefundFailed     RefundStatus = "failed"
	RefundCancelled  RefundStatus = "cancelled"
)

type Source string

const (
	SourceCallback Source = "callback"
	SourceQuery    Source = "query"
	SourceMutation Source = "mutation"
)

type Money struct {
	AmountCents int
	Currency    string
}

type Payment struct {
	Status         PaymentStatus
	Provider       string
	Merchant       string
	OrderNo        string
	ProviderTxID   string
	Money          Money
	Revision       uint64
	LastObservedAt time.Time
}

type PaymentObservation struct {
	Status         PaymentStatus
	Provider       string
	Merchant       string
	OrderNo        string
	ProviderTxID   string
	Money          Money
	Source         Source
	Authoritative  bool
	IdempotencyKey string
	PayloadDigest  string
	OccurredAt     time.Time
}

type Refund struct {
	Status           RefundStatus
	Provider         string
	Merchant         string
	OrderNo          string
	RefundNo         string
	ProviderRefundID string
	Money            Money
	Revision         uint64
	LastObservedAt   time.Time
}

type RefundObservation struct {
	Status           RefundStatus
	Provider         string
	Merchant         string
	OrderNo          string
	RefundNo         string
	ProviderRefundID string
	Money            Money
	Source           Source
	Authoritative    bool
	IdempotencyKey   string
	PayloadDigest    string
	OccurredAt       time.Time
}

func NewPayment(provider, merchant, orderNo string, money Money) (Payment, error) {
	payment := Payment{
		Status: PaymentCreated, Provider: strings.TrimSpace(provider),
		Merchant: strings.TrimSpace(merchant), OrderNo: strings.TrimSpace(orderNo),
		Money: normalizeMoney(money), Revision: 1,
	}
	if err := validatePayment(payment); err != nil {
		return Payment{}, err
	}
	return payment, nil
}

func NewRefund(provider, merchant, orderNo, refundNo string, money Money) (Refund, error) {
	refund := Refund{
		Status: RefundRequested, Provider: strings.TrimSpace(provider),
		Merchant: strings.TrimSpace(merchant), OrderNo: strings.TrimSpace(orderNo),
		RefundNo: strings.TrimSpace(refundNo), Money: normalizeMoney(money), Revision: 1,
	}
	if err := validateRefund(refund); err != nil {
		return Refund{}, err
	}
	return refund, nil
}

func ApplyPayment(current Payment, observation PaymentObservation) (Payment, bool, error) {
	if err := validatePayment(current); err != nil {
		return Payment{}, false, err
	}
	observation = normalizePaymentObservation(observation)
	if err := validatePaymentObservation(observation); err != nil {
		return Payment{}, false, err
	}
	if current.Provider != observation.Provider {
		return Payment{}, false, fmt.Errorf("%w: provider", ErrBindingConflict)
	}
	if current.Merchant != observation.Merchant {
		return Payment{}, false, fmt.Errorf("%w: merchant", ErrBindingConflict)
	}
	if current.OrderNo != observation.OrderNo {
		return Payment{}, false, fmt.Errorf("%w: order", ErrBindingConflict)
	}
	if current.Money != observation.Money {
		return Payment{}, false, fmt.Errorf("%w: money", ErrBindingConflict)
	}
	if current.ProviderTxID != "" && observation.ProviderTxID != "" &&
		current.ProviderTxID != observation.ProviderTxID {
		return Payment{}, false, ErrBindingConflict
	}
	if current.Status == observation.Status {
		return current, false, nil
	}
	if paymentTerminal(current.Status) {
		if paymentTerminal(observation.Status) {
			return Payment{}, false, fmt.Errorf(
				"%w: payment %s cannot become %s",
				ErrInvalidTransition, current.Status, observation.Status,
			)
		}
		return current, false, nil
	}
	if !paymentTransitionAllowed(current.Status, observation.Status) {
		return Payment{}, false, fmt.Errorf(
			"%w: payment %s cannot become %s",
			ErrInvalidTransition, current.Status, observation.Status,
		)
	}
	if observation.Status == PaymentSettled && observation.ProviderTxID == "" {
		return Payment{}, false, fmt.Errorf("%w: settled payment requires provider transaction id", ErrInvalidEvidence)
	}
	next := current
	next.Status = observation.Status
	if observation.ProviderTxID != "" {
		next.ProviderTxID = observation.ProviderTxID
	}
	next.LastObservedAt = observation.OccurredAt
	next.Revision++
	return next, true, nil
}

func ApplyRefund(current Refund, observation RefundObservation) (Refund, bool, error) {
	if err := validateRefund(current); err != nil {
		return Refund{}, false, err
	}
	observation = normalizeRefundObservation(observation)
	if err := validateRefundObservation(observation); err != nil {
		return Refund{}, false, err
	}
	if current.Provider != observation.Provider {
		return Refund{}, false, fmt.Errorf("%w: provider", ErrBindingConflict)
	}
	if current.Merchant != observation.Merchant {
		return Refund{}, false, fmt.Errorf("%w: merchant", ErrBindingConflict)
	}
	if current.OrderNo != observation.OrderNo {
		return Refund{}, false, fmt.Errorf("%w: order", ErrBindingConflict)
	}
	if current.RefundNo != observation.RefundNo {
		return Refund{}, false, fmt.Errorf("%w: refund", ErrBindingConflict)
	}
	if current.Money != observation.Money {
		return Refund{}, false, fmt.Errorf("%w: money", ErrBindingConflict)
	}
	if current.ProviderRefundID != "" && observation.ProviderRefundID != "" &&
		current.ProviderRefundID != observation.ProviderRefundID {
		return Refund{}, false, ErrBindingConflict
	}
	if current.Status == observation.Status {
		return current, false, nil
	}
	if refundTerminal(current.Status) {
		if refundTerminal(observation.Status) {
			return Refund{}, false, fmt.Errorf(
				"%w: refund %s cannot become %s",
				ErrInvalidTransition, current.Status, observation.Status,
			)
		}
		return current, false, nil
	}
	if !refundTransitionAllowed(current.Status, observation.Status) {
		return Refund{}, false, fmt.Errorf(
			"%w: refund %s cannot become %s",
			ErrInvalidTransition, current.Status, observation.Status,
		)
	}
	if observation.Status == RefundSucceeded && observation.ProviderRefundID == "" {
		return Refund{}, false, fmt.Errorf("%w: succeeded refund requires provider refund id", ErrInvalidEvidence)
	}
	next := current
	next.Status = observation.Status
	if observation.ProviderRefundID != "" {
		next.ProviderRefundID = observation.ProviderRefundID
	}
	next.LastObservedAt = observation.OccurredAt
	next.Revision++
	return next, true, nil
}

func validatePayment(payment Payment) error {
	if payment.Provider == "" || payment.Merchant == "" || payment.OrderNo == "" ||
		payment.Revision == 0 || !validMoney(payment.Money) {
		return ErrInvalidEvidence
	}
	switch payment.Status {
	case PaymentCreated, PaymentActionRequired, PaymentPending,
		PaymentSettled, PaymentFailed, PaymentCancelled:
		return nil
	default:
		return ErrInvalidEvidence
	}
}

func validateRefund(refund Refund) error {
	if refund.Provider == "" || refund.Merchant == "" || refund.OrderNo == "" ||
		refund.RefundNo == "" || refund.Revision == 0 || !validMoney(refund.Money) {
		return ErrInvalidEvidence
	}
	switch refund.Status {
	case RefundRequested, RefundSubmitting, RefundPending,
		RefundSucceeded, RefundFailed, RefundCancelled:
		return nil
	default:
		return ErrInvalidEvidence
	}
}

func validatePaymentObservation(observation PaymentObservation) error {
	if !observation.Authoritative || observation.IdempotencyKey == "" ||
		observation.PayloadDigest == "" || observation.OccurredAt.IsZero() ||
		observation.Provider == "" || observation.Merchant == "" ||
		observation.OrderNo == "" || !validMoney(observation.Money) {
		return ErrInvalidEvidence
	}
	switch observation.Source {
	case SourceCallback, SourceQuery, SourceMutation:
	default:
		return ErrInvalidEvidence
	}
	switch observation.Status {
	case PaymentActionRequired, PaymentPending, PaymentSettled, PaymentFailed, PaymentCancelled:
		return nil
	default:
		return ErrInvalidEvidence
	}
}

func validateRefundObservation(observation RefundObservation) error {
	if !observation.Authoritative || observation.IdempotencyKey == "" ||
		observation.PayloadDigest == "" || observation.OccurredAt.IsZero() ||
		observation.Provider == "" || observation.Merchant == "" ||
		observation.OrderNo == "" || observation.RefundNo == "" ||
		!validMoney(observation.Money) {
		return ErrInvalidEvidence
	}
	switch observation.Source {
	case SourceCallback, SourceQuery, SourceMutation:
	default:
		return ErrInvalidEvidence
	}
	switch observation.Status {
	case RefundPending, RefundSucceeded, RefundFailed:
		return nil
	default:
		return ErrInvalidEvidence
	}
}

func paymentTransitionAllowed(from, to PaymentStatus) bool {
	switch from {
	case PaymentCreated:
		return to == PaymentActionRequired || to == PaymentPending ||
			to == PaymentSettled || to == PaymentFailed || to == PaymentCancelled
	case PaymentActionRequired:
		return to == PaymentPending || to == PaymentSettled ||
			to == PaymentFailed || to == PaymentCancelled
	case PaymentPending:
		return to == PaymentSettled || to == PaymentFailed || to == PaymentCancelled
	default:
		return false
	}
}

func refundTransitionAllowed(from RefundStatus, to RefundStatus) bool {
	switch from {
	case RefundRequested:
		return to == RefundSubmitting || to == RefundPending ||
			to == RefundSucceeded || to == RefundFailed || to == RefundCancelled
	case RefundSubmitting:
		return to == RefundPending || to == RefundSucceeded || to == RefundFailed
	case RefundPending:
		return to == RefundSucceeded || to == RefundFailed
	default:
		return false
	}
}

func paymentTerminal(status PaymentStatus) bool {
	return status == PaymentSettled || status == PaymentFailed || status == PaymentCancelled
}

func refundTerminal(status RefundStatus) bool {
	return status == RefundSucceeded || status == RefundFailed || status == RefundCancelled
}

func normalizeMoney(money Money) Money {
	money.Currency = strings.ToUpper(strings.TrimSpace(money.Currency))
	return money
}

func validMoney(money Money) bool {
	return money.AmountCents > 0 && len(money.Currency) == 3
}

func normalizePaymentObservation(observation PaymentObservation) PaymentObservation {
	observation.Provider = strings.TrimSpace(observation.Provider)
	observation.Merchant = strings.TrimSpace(observation.Merchant)
	observation.OrderNo = strings.TrimSpace(observation.OrderNo)
	observation.ProviderTxID = strings.TrimSpace(observation.ProviderTxID)
	observation.IdempotencyKey = strings.TrimSpace(observation.IdempotencyKey)
	observation.PayloadDigest = strings.TrimSpace(observation.PayloadDigest)
	observation.Money = normalizeMoney(observation.Money)
	observation.OccurredAt = observation.OccurredAt.UTC()
	return observation
}

func normalizeRefundObservation(observation RefundObservation) RefundObservation {
	observation.Provider = strings.TrimSpace(observation.Provider)
	observation.Merchant = strings.TrimSpace(observation.Merchant)
	observation.OrderNo = strings.TrimSpace(observation.OrderNo)
	observation.RefundNo = strings.TrimSpace(observation.RefundNo)
	observation.ProviderRefundID = strings.TrimSpace(observation.ProviderRefundID)
	observation.IdempotencyKey = strings.TrimSpace(observation.IdempotencyKey)
	observation.PayloadDigest = strings.TrimSpace(observation.PayloadDigest)
	observation.Money = normalizeMoney(observation.Money)
	observation.OccurredAt = observation.OccurredAt.UTC()
	return observation
}
