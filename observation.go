package paykit

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidObservation = errors.New("invalid provider observation")

// NormalizePaymentQuery validates the provider-neutral query contract and
// returns a normalized copy. Provider adapters may choose transport-specific
// identifiers, but cannot weaken order, money, status, or settlement binding.
func NormalizePaymentQuery(
	input QueryPaymentIn,
	output *QueryPaymentOut,
	now time.Time,
) (*QueryPaymentOut, error) {
	if output == nil {
		return nil, invalidObservation("payment query returned no observation")
	}
	value := *output
	if value.Status == "" && value.Success {
		value.Status = PaymentStatusSettled
	}
	switch value.Status {
	case PaymentStatusPending, PaymentStatusFailed, PaymentStatusCancelled:
		if value.Success {
			return nil, invalidObservation("non-settled payment reports success")
		}
	case PaymentStatusSettled:
		if !value.Success {
			return nil, invalidObservation("settled payment does not report success")
		}
		if strings.TrimSpace(value.ProviderTxID) == "" {
			return nil, invalidObservation("settled payment is missing provider transaction id")
		}
	default:
		return nil, invalidObservation("unsupported payment status %q", value.Status)
	}
	expectedOrder := strings.TrimSpace(input.OrderNo)
	value.OrderNo = strings.TrimSpace(value.OrderNo)
	if expectedOrder == "" || value.OrderNo == "" || value.OrderNo != expectedOrder {
		return nil, invalidObservation(
			"payment order mismatch: got %q, want %q", value.OrderNo, expectedOrder,
		)
	}
	if input.AmountCents <= 0 || value.AmountCents != input.AmountCents {
		return nil, invalidObservation(
			"payment amount mismatch: got %d, want %d", value.AmountCents, input.AmountCents,
		)
	}
	expectedCurrency := strings.ToUpper(strings.TrimSpace(input.Currency))
	value.Currency = strings.ToUpper(strings.TrimSpace(value.Currency))
	if expectedCurrency == "" || value.Currency != expectedCurrency {
		return nil, invalidObservation(
			"payment currency mismatch: got %q, want %q", value.Currency, expectedCurrency,
		)
	}
	value.ProviderStatus = strings.TrimSpace(value.ProviderStatus)
	if value.ProviderStatus == "" {
		return nil, invalidObservation("payment provider status is required")
	}
	value.ObservationID = strings.TrimSpace(value.ObservationID)
	if value.ObservedAt.IsZero() {
		value.ObservedAt = now.UTC()
	} else {
		value.ObservedAt = value.ObservedAt.UTC()
	}
	return &value, nil
}

// NormalizeRefundQuery applies the same fail-closed contract to refund facts.
func NormalizeRefundQuery(
	input QueryRefundIn,
	output *QueryRefundOut,
	now time.Time,
) (*QueryRefundOut, error) {
	if output == nil {
		return nil, invalidObservation("refund query returned no observation")
	}
	value := *output
	if value.Status == "" && value.Success {
		value.Status = RefundStatusSucceeded
	}
	switch value.Status {
	case RefundStatusPending, RefundStatusFailed, RefundStatusCancelled:
		if value.Success {
			return nil, invalidObservation("non-succeeded refund reports success")
		}
	case RefundStatusSucceeded:
		if !value.Success {
			return nil, invalidObservation("succeeded refund does not report success")
		}
	default:
		return nil, invalidObservation("unsupported refund status %q", value.Status)
	}
	value.ProviderID = strings.TrimSpace(value.ProviderID)
	if value.ProviderID == "" {
		return nil, invalidObservation("refund provider id is required")
	}
	if expected := strings.TrimSpace(input.ProviderRefundID); expected != "" &&
		value.ProviderID != expected {
		return nil, invalidObservation(
			"refund id mismatch: got %q, want %q", value.ProviderID, expected,
		)
	}
	if input.AmountCents <= 0 || value.AmountCents != input.AmountCents {
		return nil, invalidObservation(
			"refund amount mismatch: got %d, want %d", value.AmountCents, input.AmountCents,
		)
	}
	expectedCurrency := strings.ToUpper(strings.TrimSpace(input.Currency))
	value.Currency = strings.ToUpper(strings.TrimSpace(value.Currency))
	if expectedCurrency == "" || value.Currency != expectedCurrency {
		return nil, invalidObservation(
			"refund currency mismatch: got %q, want %q", value.Currency, expectedCurrency,
		)
	}
	value.ProviderStatus = strings.TrimSpace(value.ProviderStatus)
	if value.ProviderStatus == "" {
		return nil, invalidObservation("refund provider status is required")
	}
	value.ObservationID = strings.TrimSpace(value.ObservationID)
	if value.ObservedAt.IsZero() {
		value.ObservedAt = now.UTC()
	} else {
		value.ObservedAt = value.ObservedAt.UTC()
	}
	return &value, nil
}

// NormalizeRefundResult validates the synchronous mutation response before it
// is admitted as a provider observation.
func NormalizeRefundResult(input RefundIn, output *RefundOut) (*RefundOut, error) {
	if output == nil {
		return nil, invalidObservation("refund mutation returned no result")
	}
	value := *output
	if value.Status == "" && value.Success {
		value.Status = RefundStatusSucceeded
	}
	switch value.Status {
	case RefundStatusPending, RefundStatusFailed, RefundStatusCancelled:
		if value.Success {
			return nil, invalidObservation("non-succeeded refund mutation reports success")
		}
	case RefundStatusSucceeded:
		if !value.Success {
			return nil, invalidObservation("succeeded refund mutation does not report success")
		}
	default:
		return nil, invalidObservation("unsupported refund mutation status %q", value.Status)
	}
	value.ProviderID = strings.TrimSpace(value.ProviderID)
	if value.ProviderID == "" {
		return nil, invalidObservation("refund mutation provider id is required")
	}
	if input.AmountCents <= 0 || value.AmountCents != input.AmountCents {
		return nil, invalidObservation(
			"refund mutation amount mismatch: got %d, want %d",
			value.AmountCents, input.AmountCents,
		)
	}
	expectedCurrency := strings.ToUpper(strings.TrimSpace(input.Currency))
	value.Currency = strings.ToUpper(strings.TrimSpace(value.Currency))
	if expectedCurrency == "" || value.Currency != expectedCurrency {
		return nil, invalidObservation(
			"refund mutation currency mismatch: got %q, want %q",
			value.Currency, expectedCurrency,
		)
	}
	value.ProviderStatus = strings.TrimSpace(value.ProviderStatus)
	if value.ProviderStatus == "" {
		return nil, invalidObservation("refund mutation provider status is required")
	}
	return &value, nil
}

func invalidObservation(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidObservation, fmt.Sprintf(format, args...))
}
