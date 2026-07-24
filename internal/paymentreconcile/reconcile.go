package paymentreconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"platform/paykit"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/paymentrecovery"
	"platform/services/commerce/internal/service"
)

var ErrQueryUnsupported = errors.New("commerce payment reconciliation: provider query unsupported")

type Reconciler struct {
	service  *service.Service
	registry paykit.Registry
	merchant func(string) string
	clock    func() time.Time
}

type Result struct {
	Order      *model.Order
	Acceptance *service.PaymentAcceptanceResult
	Queried    bool
}

func New(svc *service.Service, registry paykit.Registry) *Reconciler {
	return &Reconciler{
		service: svc, registry: registry,
		merchant: func(string) string { return "primary" },
		clock:    time.Now,
	}
}

func (reconciler *Reconciler) ReconcileOrder(ctx context.Context, orderNo string) (Result, error) {
	if reconciler == nil || reconciler.service == nil {
		return Result{}, fmt.Errorf("payment reconciler is not configured")
	}
	order, err := reconciler.service.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return Result{}, err
	}
	if order.Status == model.OrderStatusFulfilled || order.Status == model.OrderStatusRefunded {
		return Result{Order: order}, nil
	}
	provider := strings.TrimSpace(order.PaymentProvider)
	gateway, ok := reconciler.registry[provider]
	if !ok {
		return Result{}, fmt.Errorf("%w: %s is not registered", ErrQueryUnsupported, provider)
	}
	queryGateway, ok := gateway.(paykit.QueryPaymentProvider)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrQueryUnsupported, provider)
	}
	query, err := queryGateway.QueryPayment(ctx, paykit.QueryPaymentIn{
		OrderNo: order.OrderNo, SessionID: order.PaymentSessionID,
		ProviderTxID: order.ProviderTxID, AmountCents: order.AmountCents,
		Currency: order.Currency,
	})
	if err != nil {
		return Result{}, err
	}
	if query == nil {
		return Result{}, fmt.Errorf("provider payment query returned no observation")
	}
	status, ok := normalizeQueryStatus(query)
	if !ok {
		return Result{}, fmt.Errorf("provider payment query returned unsupported status %q", query.Status)
	}
	amountCents := query.AmountCents
	if amountCents <= 0 {
		amountCents = order.AmountCents
	}
	currency := strings.ToUpper(strings.TrimSpace(query.Currency))
	if currency == "" {
		currency = strings.ToUpper(strings.TrimSpace(order.Currency))
	}
	observedAt := query.ObservedAt
	if observedAt.IsZero() {
		observedAt = reconciler.clock().UTC()
	}
	evidence, err := json.Marshal(struct {
		ProviderStatus string `json:"providerStatus"`
		Status         string `json:"status"`
		OrderNo        string `json:"orderNo"`
		ProviderTxID   string `json:"providerTxId"`
		AmountCents    int    `json:"amountCents"`
		Currency       string `json:"currency"`
	}{
		ProviderStatus: query.ProviderStatus, Status: string(status),
		OrderNo: query.OrderNo, ProviderTxID: query.ProviderTxID,
		AmountCents: amountCents, Currency: currency,
	})
	if err != nil {
		return Result{}, err
	}
	digest := paymentrecovery.DigestPayload(evidence)
	idempotencyKey := strings.TrimSpace(query.ObservationID)
	if idempotencyKey == "" {
		idempotencyKey = provider + ":query:" + digest
	}
	acceptance, err := reconciler.service.AcceptPaymentObservation(
		ctx,
		paymentrecovery.PaymentObservation{
			Status: status, Provider: provider, Merchant: reconciler.merchant(provider),
			OrderNo: order.OrderNo, ProviderTxID: query.ProviderTxID,
			Money:  paymentrecovery.Money{AmountCents: amountCents, Currency: currency},
			Source: paymentrecovery.SourceQuery, Authoritative: true,
			IdempotencyKey: idempotencyKey, PayloadDigest: digest,
			OccurredAt: observedAt,
		},
		"",
		query.ProviderStatus,
	)
	if err != nil {
		return Result{}, err
	}
	updated, err := reconciler.service.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return Result{}, err
	}
	return Result{Order: updated, Acceptance: acceptance, Queried: true}, nil
}

func normalizeQueryStatus(query *paykit.QueryPaymentOut) (paymentrecovery.PaymentStatus, bool) {
	switch query.Status {
	case paykit.PaymentStatusPending:
		return paymentrecovery.PaymentPending, true
	case paykit.PaymentStatusSettled:
		return paymentrecovery.PaymentSettled, true
	case paykit.PaymentStatusFailed:
		return paymentrecovery.PaymentFailed, true
	case paykit.PaymentStatusCancelled:
		return paymentrecovery.PaymentCancelled, true
	case "":
		if query.Success {
			return paymentrecovery.PaymentSettled, true
		}
	}
	return "", false
}
