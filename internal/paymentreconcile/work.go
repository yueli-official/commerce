package paymentreconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yueli-official/foundation/go/work"

	"platform/services/commerce/internal/paymentrecovery"
)

const (
	WorkQueueReconciliation = work.Queue("payment-reconciliation")
	WorkKindScan            = work.Kind("commerce.payment-reconciliation-scan")
	WorkKindReconcile       = work.Kind("commerce.payment-reconcile")
	WorkKindRefundScan      = work.Kind("commerce.refund-reconciliation-scan")
	WorkKindRefundReconcile = work.Kind("commerce.refund-reconcile")
)

type DueAttemptStore interface {
	DuePaymentAttempts(context.Context, time.Time, int) ([]paymentrecovery.DuePaymentAttempt, error)
	DeferPaymentReconciliation(context.Context, string, time.Time, time.Time) (bool, error)
	DueRefunds(context.Context, time.Time, int) ([]paymentrecovery.DueRefund, error)
	DeferRefundReconciliation(context.Context, string, time.Time, time.Time) (bool, error)
}

type OrderReconciler interface {
	ReconcileOrder(context.Context, string) (Result, error)
	ReconcileRefund(context.Context, string) (RefundResult, error)
}

func WorkDefinition() work.Definition {
	return work.Definition{
		Version: work.DefinitionVersion,
		Queues: []work.QueueDefinition{{
			Key: WorkQueueReconciliation, Concurrency: 4,
		}},
		Kinds: []work.KindDefinition{
			{
				Key: WorkKindScan, Queue: WorkQueueReconciliation,
				DefaultAttempts: 3, MaxAttempts: 10, Timeout: 30 * time.Second,
			},
			{
				Key: WorkKindReconcile, Queue: WorkQueueReconciliation,
				DefaultAttempts: 8, MaxAttempts: 20, Timeout: 30 * time.Second,
			},
			{
				Key: WorkKindRefundScan, Queue: WorkQueueReconciliation,
				DefaultAttempts: 3, MaxAttempts: 10, Timeout: 30 * time.Second,
			},
			{
				Key: WorkKindRefundReconcile, Queue: WorkQueueReconciliation,
				DefaultAttempts: 8, MaxAttempts: 20, Timeout: 30 * time.Second,
			},
		},
		Schedules: []work.ScheduleDefinition{
			{
				Key:  "commerce-payment-reconciliation-scan",
				Cron: "*/1 * * * *", TimeZone: "UTC", Kind: WorkKindScan,
				Payload: json.RawMessage(`{"limit":100}`),
			},
			{
				Key:  "commerce-refund-reconciliation-scan",
				Cron: "*/1 * * * *", TimeZone: "UTC", Kind: WorkKindRefundScan,
				Payload: json.RawMessage(`{"limit":100}`),
			},
		},
		Retry: work.RetryPolicy{
			BaseDelay: 30 * time.Second, MaxDelay: 30 * time.Minute, Jitter: 0.2,
		},
	}
}

func WorkHandlers(
	store DueAttemptStore,
	enqueuer work.Enqueuer,
	reconciler OrderReconciler,
	clock func() time.Time,
) map[work.Kind]work.Handler {
	if clock == nil {
		clock = time.Now
	}
	return map[work.Kind]work.Handler{
		WorkKindScan: work.HandlerFunc(func(
			ctx context.Context,
			job work.Job,
			_ work.Progress,
		) (work.Result, error) {
			if store == nil || enqueuer == nil {
				return work.Result{}, errors.New("payment reconciliation scan dependencies are required")
			}
			var payload struct {
				Limit int `json:"limit"`
			}
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return work.Result{}, work.Permanent(err)
			}
			now := clock().UTC()
			attempts, err := store.DuePaymentAttempts(ctx, now, payload.Limit)
			if err != nil {
				return work.Result{}, err
			}
			enqueued := 0
			for _, attempt := range attempts {
				payload, err := json.Marshal(map[string]string{"orderNo": attempt.OrderNo})
				if err != nil {
					return work.Result{}, err
				}
				bucket := attempt.NextReconcileAt.UTC().Format("20060102T150405.000000000Z")
				_, err = enqueuer.Enqueue(ctx, work.Request{
					Kind: WorkKindReconcile, Payload: payload,
					IdempotencyKey: fmt.Sprintf(
						"commerce:payment-reconcile:%s:%s", attempt.ID, bucket,
					),
				})
				if err != nil {
					return work.Result{}, err
				}
				advanced, err := store.DeferPaymentReconciliation(
					ctx, attempt.ID, attempt.NextReconcileAt, now.Add(15*time.Minute),
				)
				if err != nil {
					return work.Result{}, err
				}
				if advanced {
					enqueued++
				}
			}
			data, _ := json.Marshal(map[string]int{"enqueued": enqueued})
			return work.Result{Summary: fmt.Sprintf("enqueued %d payment reconciliation job(s)", enqueued), Data: data}, nil
		}),
		WorkKindReconcile: work.HandlerFunc(func(
			ctx context.Context,
			job work.Job,
			_ work.Progress,
		) (work.Result, error) {
			if reconciler == nil {
				return work.Result{}, errors.New("payment reconciler is required")
			}
			var payload struct {
				OrderNo string `json:"orderNo"`
			}
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return work.Result{}, work.Permanent(err)
			}
			if payload.OrderNo == "" {
				return work.Result{}, work.Permanent(errors.New("orderNo is required"))
			}
			result, err := reconciler.ReconcileOrder(ctx, payload.OrderNo)
			if err != nil {
				if errors.Is(err, ErrQueryUnsupported) {
					return work.Result{}, work.Permanent(err)
				}
				return work.Result{}, err
			}
			data, _ := json.Marshal(map[string]any{
				"orderNo": payload.OrderNo,
				"queried": result.Queried,
				"status":  result.Order.PaymentState,
			})
			return work.Result{Summary: "payment reconciliation completed", Data: data}, nil
		}),
		WorkKindRefundScan: work.HandlerFunc(func(
			ctx context.Context,
			job work.Job,
			_ work.Progress,
		) (work.Result, error) {
			if store == nil || enqueuer == nil {
				return work.Result{}, errors.New("refund reconciliation scan dependencies are required")
			}
			var payload struct {
				Limit int `json:"limit"`
			}
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return work.Result{}, work.Permanent(err)
			}
			now := clock().UTC()
			refunds, err := store.DueRefunds(ctx, now, payload.Limit)
			if err != nil {
				return work.Result{}, err
			}
			enqueued := 0
			for _, refund := range refunds {
				payload, err := json.Marshal(map[string]string{"refundNo": refund.RefundNo})
				if err != nil {
					return work.Result{}, err
				}
				bucket := refund.NextReconcileAt.UTC().Format("20060102T150405.000000000Z")
				_, err = enqueuer.Enqueue(ctx, work.Request{
					Kind: WorkKindRefundReconcile, Payload: payload,
					IdempotencyKey: fmt.Sprintf(
						"commerce:refund-reconcile:%s:%s", refund.ID, bucket,
					),
				})
				if err != nil {
					return work.Result{}, err
				}
				advanced, err := store.DeferRefundReconciliation(
					ctx, refund.ID, refund.NextReconcileAt, now.Add(15*time.Minute),
				)
				if err != nil {
					return work.Result{}, err
				}
				if advanced {
					enqueued++
				}
			}
			data, _ := json.Marshal(map[string]int{"enqueued": enqueued})
			return work.Result{
				Summary: fmt.Sprintf("enqueued %d refund reconciliation job(s)", enqueued),
				Data:    data,
			}, nil
		}),
		WorkKindRefundReconcile: work.HandlerFunc(func(
			ctx context.Context,
			job work.Job,
			_ work.Progress,
		) (work.Result, error) {
			if reconciler == nil {
				return work.Result{}, errors.New("refund reconciler is required")
			}
			var payload struct {
				RefundNo string `json:"refundNo"`
			}
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return work.Result{}, work.Permanent(err)
			}
			if payload.RefundNo == "" {
				return work.Result{}, work.Permanent(errors.New("refundNo is required"))
			}
			result, err := reconciler.ReconcileRefund(ctx, payload.RefundNo)
			if err != nil {
				if errors.Is(err, ErrQueryUnsupported) {
					return work.Result{}, work.Permanent(err)
				}
				return work.Result{}, err
			}
			data, _ := json.Marshal(map[string]any{
				"refundNo": payload.RefundNo,
				"queried":  result.Queried,
				"status":   result.Refund.Status,
			})
			return work.Result{
				Summary: "refund reconciliation completed",
				Data:    data,
			}, nil
		}),
	}
}
