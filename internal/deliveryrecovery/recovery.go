// Package deliveryrecovery owns the durable recovery protocol for short-lived
// delivery grants minted in the remote Asset service.
package deliveryrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yueli-official/foundation/go/work"
)

const (
	StateActive        = "active"
	StateRevokePending = "revoke_pending"
	StateRevoked       = "revoked"
	StateExpired       = "expired"

	WorkKindScan   = work.Kind("commerce.asset-grant-revocation-scan")
	WorkKindRevoke = work.Kind("commerce.asset-grant-revoke")
)

type Grant struct {
	ID              string     `json:"id" orm:"id"`
	OrderID         string     `json:"orderId" orm:"order_id"`
	DeliveryGrantID string     `json:"deliveryGrantId" orm:"delivery_grant_id"`
	AssetID         string     `json:"assetId" orm:"asset_id"`
	ProviderGrantID string     `json:"providerGrantId" orm:"provider_grant_id"`
	State           string     `json:"state" orm:"state"`
	ExpiresAt       time.Time  `json:"expiresAt" orm:"expires_at"`
	NextRevokeAt    *time.Time `json:"nextRevokeAt,omitempty" orm:"next_revoke_at"`
	RevokeAttempts  int        `json:"revokeAttempts" orm:"revoke_attempts"`
	LastError       string     `json:"lastError,omitempty" orm:"last_error"`
	CreatedAt       time.Time  `json:"createdAt" orm:"created_at"`
	UpdatedAt       time.Time  `json:"updatedAt" orm:"updated_at"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty" orm:"revoked_at"`
}

type Store interface {
	DueAssetGrantRevocations(context.Context, time.Time, int) ([]Grant, error)
	DeferAssetGrantRevocation(context.Context, string, time.Time, time.Time) (bool, error)
	AssetDeliveryGrant(context.Context, string) (*Grant, error)
	MarkAssetGrantRevoked(context.Context, string) error
	MarkAssetGrantRevokeFailed(context.Context, string, string) (int, error)
}

type Revoker interface {
	RevokeDelivery(context.Context, string) error
}

type FailureNotice struct {
	GrantID         string
	OrderID         string
	ProviderGrantID string
	Attempts        int
	Error           string
}

type FailureNotifier interface {
	NotifyDeliveryRevocationFailed(context.Context, FailureNotice)
}

// ExtendWorkDefinition adds delivery recovery without coupling the payment
// reconciliation module to Asset HTTP semantics.
func ExtendWorkDefinition(definition work.Definition, queue work.Queue) work.Definition {
	definition.Kinds = append(definition.Kinds,
		work.KindDefinition{
			Key: WorkKindScan, Queue: queue,
			DefaultAttempts: 3, MaxAttempts: 10, Timeout: 30 * time.Second,
		},
		work.KindDefinition{
			Key: WorkKindRevoke, Queue: queue,
			DefaultAttempts: 8, MaxAttempts: 20, Timeout: 30 * time.Second,
		},
	)
	definition.Schedules = append(definition.Schedules, work.ScheduleDefinition{
		Key:  "commerce-asset-grant-revocation-scan",
		Cron: "*/1 * * * *", TimeZone: "UTC", Kind: WorkKindScan,
		Payload: json.RawMessage(`{"limit":100}`),
	})
	return definition
}

func WorkHandlers(
	store Store,
	enqueuer work.Enqueuer,
	revoker Revoker,
	notifier FailureNotifier,
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
				return work.Result{}, errors.New("asset grant recovery scan dependencies are required")
			}
			var payload struct {
				Limit int `json:"limit"`
			}
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return work.Result{}, work.Permanent(err)
			}
			now := clock().UTC()
			grants, err := store.DueAssetGrantRevocations(ctx, now, payload.Limit)
			if err != nil {
				return work.Result{}, err
			}
			enqueued := 0
			for _, grant := range grants {
				raw, err := json.Marshal(map[string]string{"grantId": grant.ID})
				if err != nil {
					return work.Result{}, err
				}
				due := now
				if grant.NextRevokeAt != nil {
					due = grant.NextRevokeAt.UTC()
				}
				bucket := due.Format("20060102T150405.000000000Z")
				if _, err = enqueuer.Enqueue(ctx, work.Request{
					Kind: WorkKindRevoke, Payload: raw,
					IdempotencyKey: fmt.Sprintf("commerce:asset-grant-revoke:%s:%s", grant.ID, bucket),
				}); err != nil {
					return work.Result{}, err
				}
				advanced, err := store.DeferAssetGrantRevocation(
					ctx, grant.ID, due, now.Add(15*time.Minute),
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
				Summary: fmt.Sprintf("enqueued %d asset grant revocation job(s)", enqueued),
				Data:    data,
			}, nil
		}),
		WorkKindRevoke: work.HandlerFunc(func(
			ctx context.Context,
			job work.Job,
			_ work.Progress,
		) (work.Result, error) {
			if store == nil || revoker == nil {
				return work.Result{}, errors.New("asset grant revocation dependencies are required")
			}
			var payload struct {
				GrantID string `json:"grantId"`
			}
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return work.Result{}, work.Permanent(err)
			}
			payload.GrantID = strings.TrimSpace(payload.GrantID)
			if payload.GrantID == "" {
				return work.Result{}, work.Permanent(errors.New("grantId is required"))
			}
			grant, err := store.AssetDeliveryGrant(ctx, payload.GrantID)
			if err != nil {
				return work.Result{}, err
			}
			if grant == nil || grant.State == StateRevoked || grant.State == StateExpired {
				return work.Result{Summary: "asset grant revocation already converged"}, nil
			}
			if err := revoker.RevokeDelivery(ctx, grant.ProviderGrantID); err != nil {
				attempts, markErr := store.MarkAssetGrantRevokeFailed(ctx, grant.ID, err.Error())
				if markErr != nil {
					return work.Result{}, errors.Join(err, markErr)
				}
				if notifier != nil && attempts >= 3 {
					notifier.NotifyDeliveryRevocationFailed(ctx, FailureNotice{
						GrantID: grant.ID, OrderID: grant.OrderID,
						ProviderGrantID: grant.ProviderGrantID,
						Attempts:        attempts, Error: err.Error(),
					})
				}
				return work.Result{}, err
			}
			if err := store.MarkAssetGrantRevoked(ctx, grant.ID); err != nil {
				return work.Result{}, err
			}
			data, _ := json.Marshal(map[string]string{
				"grantId": grant.ID, "providerGrantId": grant.ProviderGrantID,
			})
			return work.Result{Summary: "asset grant revoked", Data: data}, nil
		}),
	}
}
