package paymentreconcile

import (
	"context"
	"testing"
	"time"

	"github.com/yueli-official/foundation/go/work"

	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/paymentrecovery"
)

type workTestStore struct {
	due      []paymentrecovery.DuePaymentAttempt
	deferred int
}

func (store *workTestStore) DuePaymentAttempts(
	context.Context,
	time.Time,
	int,
) ([]paymentrecovery.DuePaymentAttempt, error) {
	return append([]paymentrecovery.DuePaymentAttempt(nil), store.due...), nil
}

func (store *workTestStore) DeferPaymentReconciliation(
	context.Context,
	string,
	time.Time,
	time.Time,
) (bool, error) {
	store.deferred++
	return true, nil
}

type workTestReconciler struct {
	orders []string
}

func (reconciler *workTestReconciler) ReconcileOrder(
	_ context.Context,
	orderNo string,
) (Result, error) {
	reconciler.orders = append(reconciler.orders, orderNo)
	return Result{
		Order:   &model.Order{OrderNo: orderNo, PaymentState: string(paymentrecovery.PaymentPending)},
		Queried: true,
	}, nil
}

func TestPaymentReconciliationWorkScansAndExecutes(t *testing.T) {
	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	catalog := work.MustCompile(WorkDefinition())
	backend, err := work.NewMemory(catalog, work.MemoryOptions{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	store := &workTestStore{due: []paymentrecovery.DuePaymentAttempt{{
		ID: "attempt-1", OrderNo: "order-1", Provider: "alipay",
		NextReconcileAt: now.Add(-time.Minute),
	}}}
	reconciler := &workTestReconciler{}
	runner, err := work.NewRunner(
		catalog,
		backend,
		WorkHandlers(store, backend, reconciler, func() time.Time { return now }),
		work.RunnerOptions{
			WorkerID: "test-worker", Clock: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Enqueue(context.Background(), work.Request{
		Kind: WorkKindScan, Payload: []byte(`{"limit":10}`),
	}); err != nil {
		t.Fatal(err)
	}
	worked, err := runner.RunOnce(context.Background(), WorkQueueReconciliation, "test-worker/scan")
	if err != nil || !worked {
		t.Fatalf("scan worked=%v err=%v", worked, err)
	}
	worked, err = runner.RunOnce(context.Background(), WorkQueueReconciliation, "test-worker/reconcile")
	if err != nil || !worked {
		t.Fatalf("reconcile worked=%v err=%v", worked, err)
	}
	if store.deferred != 1 {
		t.Fatalf("deferred = %d, want 1", store.deferred)
	}
	if len(reconciler.orders) != 1 || reconciler.orders[0] != "order-1" {
		t.Fatalf("reconciled orders = %v", reconciler.orders)
	}
	stats, err := backend.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.ByStatus[work.StatusSucceeded] != 2 {
		t.Fatalf("work stats = %+v", stats.ByStatus)
	}
}
