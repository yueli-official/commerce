package service_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"platform/paykit"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/paymentreconcile"
	"platform/services/commerce/internal/paymentrecovery"
	"platform/services/commerce/internal/service"
)

func TestPaymentObservationReplaysAndRepairsLocalCancellation(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub: "recovery-user", Provider: "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey: "shop", ExternalID: uid("recovery-product"),
			VariantID: uid("recovery-variant"), Title: "Recovery Product",
			PriceCents: 1900, Currency: "CNY", DeliveryKind: "asset_file",
			DeliveryRef: "asset-recovery", Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if err := svc.CancelOrder(ctx, order.OrderNo); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}

	observation := paymentrecovery.PaymentObservation{
		Status: paymentrecovery.PaymentSettled, Provider: "alipay", Merchant: "primary",
		OrderNo: order.OrderNo, ProviderTxID: "ALI-TX-RECOVERY",
		Money:  paymentrecovery.Money{AmountCents: order.AmountCents, Currency: order.Currency},
		Source: paymentrecovery.SourceCallback, Authoritative: true,
		IdempotencyKey: "ALI-EVENT-RECOVERY",
		PayloadDigest:  paymentrecovery.DigestPayload([]byte("signed-event")),
		OccurredAt:     time.Now().UTC(),
	}
	first, err := svc.AcceptPaymentObservation(ctx, observation, "ALI-EVENT-RECOVERY", "TRADE_SUCCESS")
	if err != nil {
		t.Fatalf("AcceptPaymentObservation: %v", err)
	}
	if first.Processing != paymentrecovery.ProcessingApplied || first.Grant == nil {
		event, eventErr := pg.ProviderEventByKey(ctx, "alipay", "primary", "ALI-EVENT-RECOVERY")
		t.Fatalf("first result = %+v event=%+v eventErr=%v", first, event, eventErr)
	}
	replay, err := svc.AcceptPaymentObservation(ctx, observation, "ALI-EVENT-RECOVERY", "TRADE_SUCCESS")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Processing != paymentrecovery.ProcessingApplied || replay.Grant != nil {
		t.Fatalf("replay result = %+v", replay)
	}

	settled, err := svc.GetOrderByNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != model.OrderStatusFulfilled {
		t.Fatalf("late settlement order status = %q", settled.Status)
	}
	items, err := svc.OrderItems(ctx, order.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("order items = %+v err=%v", items, err)
	}
	count, err := pg.EntitlementCount(ctx, "recovery-user", items[0].ProductID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("entitlement count = %d", count)
	}
}

func TestPaymentObservationRejectsSameEventKeyWithDifferentEvidence(t *testing.T) {
	svc, _, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub: "conflict-user", Provider: "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey: "shop", ExternalID: uid("conflict-product"),
			VariantID: uid("conflict-variant"), Title: "Conflict Product",
			PriceCents: 500, Currency: "CNY", DeliveryKind: "asset_file",
			DeliveryRef: "asset-conflict", Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := paymentrecovery.PaymentObservation{
		Status: paymentrecovery.PaymentPending, Provider: "alipay", Merchant: "primary",
		OrderNo: order.OrderNo,
		Money:   paymentrecovery.Money{AmountCents: order.AmountCents, Currency: order.Currency},
		Source:  paymentrecovery.SourceCallback, Authoritative: true,
		IdempotencyKey: "ALI-EVENT-CONFLICT",
		PayloadDigest:  paymentrecovery.DigestPayload([]byte("first-body")),
		OccurredAt:     time.Now().UTC(),
	}
	if _, err := svc.AcceptPaymentObservation(ctx, observation, "ALI-EVENT-CONFLICT", "WAIT_BUYER_PAY"); err != nil {
		t.Fatal(err)
	}
	observation.PayloadDigest = paymentrecovery.DigestPayload([]byte("tampered-body"))
	if _, err := svc.AcceptPaymentObservation(ctx, observation, "ALI-EVENT-CONFLICT", "WAIT_BUYER_PAY"); !errors.Is(err, paymentrecovery.ErrEventConflict) {
		t.Fatalf("conflicting replay error = %v", err)
	}
}

func TestPaymentObservationConcurrentReplayGrantsOnce(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub: "concurrent-user", Provider: "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey: "shop", ExternalID: uid("concurrent-product"),
			VariantID: uid("concurrent-variant"), Title: "Concurrent Product",
			PriceCents: 700, Currency: "CNY", DeliveryKind: "asset_file",
			DeliveryRef: "asset-concurrent", Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := paymentrecovery.PaymentObservation{
		Status: paymentrecovery.PaymentSettled, Provider: "alipay", Merchant: "primary",
		OrderNo: order.OrderNo, ProviderTxID: "ALI-TX-CONCURRENT",
		Money: paymentrecovery.Money{
			AmountCents: order.AmountCents,
			Currency:    order.Currency,
		},
		Source: paymentrecovery.SourceCallback, Authoritative: true,
		IdempotencyKey: "ALI-EVENT-CONCURRENT",
		PayloadDigest:  paymentrecovery.DigestPayload([]byte("concurrent-signed-event")),
		OccurredAt:     time.Now().UTC(),
	}

	const workers = 8
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, acceptErr := svc.AcceptPaymentObservation(
				ctx, observation, "ALI-EVENT-CONCURRENT", "TRADE_SUCCESS",
			)
			errs <- acceptErr
		}()
	}
	wait.Wait()
	close(errs)
	for acceptErr := range errs {
		if acceptErr != nil {
			t.Fatalf("concurrent replay: %v", acceptErr)
		}
	}

	items, err := svc.OrderItems(ctx, order.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("order items = %+v err=%v", items, err)
	}
	entitlements, err := pg.EntitlementCount(ctx, "concurrent-user", items[0].ProductID)
	if err != nil || entitlements != 1 {
		t.Fatalf("entitlements = %d err=%v", entitlements, err)
	}
	grants, err := pg.DeliveryGrantsByOrderID(ctx, order.ID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("delivery grants = %d err=%v", len(grants), err)
	}
}

func TestFullRefundRevokesEntitlementWithoutOverwritingPaymentID(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub: "refund-user", Provider: "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey: "shop", ExternalID: uid("refund-product"),
			VariantID: uid("refund-variant"), Title: "Refund Product",
			PriceCents: 900, Currency: "CNY", DeliveryKind: "asset_file",
			DeliveryRef: "asset-refund", Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SettleCheckout(
		ctx, order.OrderNo, "alipay", "ALI-PAYMENT-TX", order.AmountCents,
	); err != nil {
		t.Fatal(err)
	}
	reserved, err := svc.RequestRefund(ctx, service.RefundRequestInput{
		OrderNo: order.OrderNo, Reason: "customer request",
		RequestedBy: "admin-1", IdempotencyKey: "refund-idem-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := svc.RequestRefund(ctx, service.RefundRequestInput{
		OrderNo: order.OrderNo, Reason: "customer request",
		RequestedBy: "admin-1", IdempotencyKey: "refund-idem-1",
	})
	if err != nil || !duplicate.Duplicate || duplicate.Refund.ID != reserved.Refund.ID {
		t.Fatalf("duplicate = %+v err=%v", duplicate, err)
	}
	if err := svc.MarkRefundSubmitting(ctx, reserved.Refund.RefundNo); err != nil {
		t.Fatal(err)
	}
	accepted, err := svc.AcceptRefundObservation(ctx, paymentrecovery.RefundObservation{
		Status: paymentrecovery.RefundSucceeded, Provider: "alipay", Merchant: "primary",
		OrderNo: order.OrderNo, RefundNo: reserved.Refund.RefundNo,
		ProviderRefundID: "ALI-REFUND-1",
		Money: paymentrecovery.Money{
			AmountCents: order.AmountCents, Currency: order.Currency,
		},
		Source: paymentrecovery.SourceMutation, Authoritative: true,
		IdempotencyKey: "refund-result-1",
		PayloadDigest:  paymentrecovery.DigestPayload([]byte("refund-success")),
		OccurredAt:     time.Now().UTC(),
	}, "SUCCESS")
	if err != nil || accepted.Status != paymentrecovery.RefundSucceeded {
		t.Fatalf("accepted = %+v err=%v", accepted, err)
	}
	refunded, err := svc.GetOrderByNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if refunded.Status != model.OrderStatusRefunded ||
		refunded.RefundedAmount != order.AmountCents ||
		refunded.ProviderTxID != "ALI-PAYMENT-TX" {
		t.Fatalf("refunded order = %+v", refunded)
	}
	items, err := svc.OrderItems(ctx, order.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %+v err=%v", items, err)
	}
	entitled, err := pg.EntitlementExists(ctx, "refund-user", items[0].ProductID)
	if err != nil || entitled {
		t.Fatalf("active entitlement = %v err=%v", entitled, err)
	}
	grants, err := pg.DeliveryGrantsByOrderID(ctx, order.ID)
	if err != nil || len(grants) != 1 || grants[0].State != "revoked" {
		t.Fatalf("grants = %+v err=%v", grants, err)
	}
}

func TestPartialRefundKeepsDeliveryActive(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub: "partial-user", Provider: "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey: "shop", ExternalID: uid("partial-product"),
			VariantID: uid("partial-variant"), Title: "Partial Product",
			PriceCents: 800, Currency: "CNY", DeliveryKind: "asset_file",
			DeliveryRef: "asset-partial", Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SettleCheckout(
		ctx, order.OrderNo, "alipay", "ALI-PARTIAL-TX", order.AmountCents,
	); err != nil {
		t.Fatal(err)
	}
	amount := order.AmountCents / 2
	reserved, err := svc.RequestRefund(ctx, service.RefundRequestInput{
		OrderNo: order.OrderNo, AmountCents: amount, Reason: "goodwill",
		RequestedBy: "admin-1", IdempotencyKey: "partial-idem-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkRefundSubmitting(ctx, reserved.Refund.RefundNo); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptRefundObservation(ctx, paymentrecovery.RefundObservation{
		Status: paymentrecovery.RefundSucceeded, Provider: "alipay", Merchant: "primary",
		OrderNo: order.OrderNo, RefundNo: reserved.Refund.RefundNo,
		ProviderRefundID: "ALI-PARTIAL-REFUND",
		Money:            paymentrecovery.Money{AmountCents: amount, Currency: order.Currency},
		Source:           paymentrecovery.SourceMutation, Authoritative: true,
		IdempotencyKey: "partial-result-1",
		PayloadDigest:  paymentrecovery.DigestPayload([]byte("partial-success")),
		OccurredAt:     time.Now().UTC(),
	}, "SUCCESS"); err != nil {
		t.Fatal(err)
	}
	updated, err := svc.GetOrderByNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.OrderStatusFulfilled || updated.RefundedAmount != amount {
		t.Fatalf("partial order = %+v", updated)
	}
	items, _ := svc.OrderItems(ctx, order.ID)
	entitled, err := pg.EntitlementExists(ctx, "partial-user", items[0].ProductID)
	if err != nil || !entitled {
		t.Fatalf("active entitlement = %v err=%v", entitled, err)
	}
}

func TestPendingRefundReconcilesToSuccess(t *testing.T) {
	svc, _, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub: "refund-reconcile-user", Provider: "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey: "shop", ExternalID: uid("refund-reconcile-product"),
			VariantID: uid("refund-reconcile-variant"), Title: "Refund Reconcile Product",
			PriceCents: 1200, Currency: "CNY", DeliveryKind: "asset_file",
			DeliveryRef: "asset-refund-reconcile", Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SettleCheckout(
		ctx, order.OrderNo, "alipay", "ALIPAY-TRADE-1", order.AmountCents,
	); err != nil {
		t.Fatal(err)
	}
	reserved, err := svc.RequestRefund(ctx, service.RefundRequestInput{
		OrderNo: order.OrderNo, Reason: "customer request",
		RequestedBy: "admin-1", IdempotencyKey: uid("refund-reconcile-idem"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkRefundSubmitting(ctx, reserved.Refund.RefundNo); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptRefundObservation(ctx, paymentrecovery.RefundObservation{
		Status: paymentrecovery.RefundPending, Provider: "alipay", Merchant: "primary",
		OrderNo: order.OrderNo, RefundNo: reserved.Refund.RefundNo,
		ProviderRefundID: "ALIPAY-REFUND-1",
		Money: paymentrecovery.Money{
			AmountCents: order.AmountCents, Currency: order.Currency,
		},
		Source: paymentrecovery.SourceMutation, Authoritative: true,
		IdempotencyKey: uid("refund-pending"),
		PayloadDigest:  paymentrecovery.DigestPayload([]byte("refund-pending")),
		OccurredAt:     time.Now().UTC().Add(-time.Minute),
	}, "PENDING"); err != nil {
		t.Fatal(err)
	}
	provider := paykit.NewFakeProvider("alipay")
	provider.NextRefundQuery = paykit.QueryRefundOut{
		Success: true, ProviderID: "ALIPAY-REFUND-1",
		AmountCents: order.AmountCents, Currency: order.Currency,
		ObservationID: "alipay:refund-query:ALIPAY-REFUND-1:REFUND_SUCCESS",
		Status:        paykit.RefundStatusSucceeded, ProviderStatus: "REFUND_SUCCESS",
		ObservedAt: time.Now().UTC(),
	}
	registry := paykit.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	result, err := paymentreconcile.New(svc, registry).ReconcileRefund(
		ctx, reserved.Refund.RefundNo,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Queried || result.Refund.Status != paymentrecovery.RefundSucceeded {
		t.Fatalf("reconcile result = %+v", result)
	}
	if len(provider.RefundQueryCalls) != 1 ||
		provider.RefundQueryCalls[0].ProviderRefundID != "ALIPAY-REFUND-1" {
		t.Fatalf("refund query calls = %+v", provider.RefundQueryCalls)
	}
	updated, err := svc.GetOrderByNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.OrderStatusRefunded ||
		updated.RefundedAmount != order.AmountCents {
		t.Fatalf("reconciled order = %+v", updated)
	}
}

func TestDisputeSuspendsAndRestoresAccessWhenSellerWins(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	if _, err := svc.SavePaymentMethods(ctx, []service.PaymentMethodInput{{
		Provider: "paypal", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub: "dispute-win-user", Provider: "paypal",
		Items: []service.CheckoutItemDesc{{
			SiteKey: "shop", ExternalID: uid("dispute-win-product"),
			VariantID: uid("dispute-win-variant"), Title: "Dispute Win Product",
			PriceCents: 1400, Currency: "USD", DeliveryKind: "asset_file",
			DeliveryRef: "asset-dispute-win", Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SettleCheckout(
		ctx, order.OrderNo, "paypal", "PAYPAL-CAPTURE-WIN", order.AmountCents,
	); err != nil {
		t.Fatal(err)
	}
	opened := disputeServiceObservation(
		order, "PAYPAL-CAPTURE-WIN", "PP-D-WIN",
		paymentrecovery.DisputeNeedsResponse, "WH-D-WIN-OPEN",
	)
	accepted, err := svc.AcceptDisputeObservation(ctx, opened)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Processing != paymentrecovery.ProcessingApplied ||
		accepted.Dispute.Status != paymentrecovery.DisputeNeedsResponse {
		t.Fatalf("opened dispute = %+v", accepted)
	}
	duplicate, err := svc.AcceptDisputeObservation(ctx, opened)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate dispute = %+v err=%v", duplicate, err)
	}
	items, err := svc.OrderItems(ctx, order.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %+v err=%v", items, err)
	}
	active, err := pg.EntitlementExists(
		ctx, "dispute-win-user", items[0].ProductID,
	)
	if err != nil || active {
		t.Fatalf("active entitlement while disputed = %v err=%v", active, err)
	}
	grants, err := pg.DeliveryGrantsByOrderID(ctx, order.ID)
	if err != nil || len(grants) != 1 || grants[0].State != "suspended" {
		t.Fatalf("suspended grants = %+v err=%v", grants, err)
	}
	won := disputeServiceObservation(
		order, "PAYPAL-CAPTURE-WIN", "PP-D-WIN",
		paymentrecovery.DisputeWon, "WH-D-WIN-RESOLVED",
	)
	won.ProviderStatus = "RESOLVED"
	won.OutcomeCode = "RESOLVED_SELLER_FAVOUR"
	if _, err := svc.AcceptDisputeObservation(ctx, won); err != nil {
		t.Fatal(err)
	}
	active, err = pg.EntitlementExists(
		ctx, "dispute-win-user", items[0].ProductID,
	)
	if err != nil || !active {
		t.Fatalf("restored entitlement = %v err=%v", active, err)
	}
	grants, err = pg.DeliveryGrantsByOrderID(ctx, order.ID)
	if err != nil || len(grants) != 1 || grants[0].State != "active" {
		t.Fatalf("restored grants = %+v err=%v", grants, err)
	}
	updated, err := svc.GetOrderByNo(ctx, order.OrderNo)
	if err != nil || updated.DisputeState != string(paymentrecovery.DisputeWon) ||
		updated.DeliveryState != model.DeliveryStateGranted {
		t.Fatalf("won order = %+v err=%v", updated, err)
	}
}

func TestDisputeLossPermanentlyRevokesAccess(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	if _, err := svc.SavePaymentMethods(ctx, []service.PaymentMethodInput{{
		Provider: "paypal", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub: "dispute-loss-user", Provider: "paypal",
		Items: []service.CheckoutItemDesc{{
			SiteKey: "shop", ExternalID: uid("dispute-loss-product"),
			VariantID: uid("dispute-loss-variant"), Title: "Dispute Loss Product",
			PriceCents: 1500, Currency: "USD", DeliveryKind: "asset_file",
			DeliveryRef: "asset-dispute-loss", Quantity: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SettleCheckout(
		ctx, order.OrderNo, "paypal", "PAYPAL-CAPTURE-LOSS", order.AmountCents,
	); err != nil {
		t.Fatal(err)
	}
	opened := disputeServiceObservation(
		order, "PAYPAL-CAPTURE-LOSS", "PP-D-LOSS",
		paymentrecovery.DisputeOpen, "WH-D-LOSS-OPEN",
	)
	if _, err := svc.AcceptDisputeObservation(ctx, opened); err != nil {
		t.Fatal(err)
	}
	lost := disputeServiceObservation(
		order, "PAYPAL-CAPTURE-LOSS", "PP-D-LOSS",
		paymentrecovery.DisputeLost, "WH-D-LOSS-RESOLVED",
	)
	lost.ProviderStatus = "RESOLVED"
	lost.OutcomeCode = "RESOLVED_BUYER_FAVOUR"
	if _, err := svc.AcceptDisputeObservation(ctx, lost); err != nil {
		t.Fatal(err)
	}
	items, _ := svc.OrderItems(ctx, order.ID)
	active, err := pg.EntitlementExists(
		ctx, "dispute-loss-user", items[0].ProductID,
	)
	if err != nil || active {
		t.Fatalf("active entitlement after loss = %v err=%v", active, err)
	}
	grants, err := pg.DeliveryGrantsByOrderID(ctx, order.ID)
	if err != nil || len(grants) != 1 || grants[0].State != "revoked" {
		t.Fatalf("revoked grants = %+v err=%v", grants, err)
	}
	updated, err := svc.GetOrderByNo(ctx, order.OrderNo)
	if err != nil || updated.DisputeState != string(paymentrecovery.DisputeLost) ||
		updated.DeliveryState != model.DeliveryStateRevoked {
		t.Fatalf("lost order = %+v err=%v", updated, err)
	}
}

func disputeServiceObservation(
	order *model.Order,
	providerTxID, disputeID string,
	status paymentrecovery.DisputeStatus,
	eventID string,
) paymentrecovery.DisputeObservation {
	now := time.Now().UTC()
	return paymentrecovery.DisputeObservation{
		Status: status, Provider: "paypal", Merchant: "primary",
		ProviderTxID: providerTxID, ProviderDisputeID: disputeID,
		ProviderStatus: string(status),
		Money: paymentrecovery.Money{
			AmountCents: order.AmountCents, Currency: order.Currency,
		},
		ReasonCode: "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
		OpenedAt:   now.Add(-time.Hour), DueAt: now.Add(7 * 24 * time.Hour),
		Source: paymentrecovery.SourceCallback, Authoritative: true,
		IdempotencyKey: eventID,
		PayloadDigest:  paymentrecovery.DigestPayload([]byte(eventID)),
		OccurredAt:     now,
	}
}
