package deliveryrecovery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yueli-official/foundation/go/work"

	"platform/services/commerce/internal/deliveryrecovery"
)

type memoryStore struct {
	grant    deliveryrecovery.Grant
	deferred int
}

func (store *memoryStore) DueAssetGrantRevocations(
	context.Context, time.Time, int,
) ([]deliveryrecovery.Grant, error) {
	return []deliveryrecovery.Grant{store.grant}, nil
}

func (store *memoryStore) DeferAssetGrantRevocation(
	_ context.Context, _ string, _ time.Time, next time.Time,
) (bool, error) {
	store.deferred++
	store.grant.NextRevokeAt = &next
	return true, nil
}

func (store *memoryStore) AssetDeliveryGrant(
	context.Context, string,
) (*deliveryrecovery.Grant, error) {
	copy := store.grant
	return &copy, nil
}

func (store *memoryStore) MarkAssetGrantRevoked(context.Context, string) error {
	store.grant.State = deliveryrecovery.StateRevoked
	return nil
}

func (store *memoryStore) MarkAssetGrantRevokeFailed(
	context.Context, string, string,
) (int, error) {
	store.grant.RevokeAttempts++
	return store.grant.RevokeAttempts, nil
}

type captureRevoker struct {
	ids []string
	err error
}

func (revoker *captureRevoker) RevokeDelivery(_ context.Context, id string) error {
	revoker.ids = append(revoker.ids, id)
	return revoker.err
}

func TestWorkScansAndRevokesRemoteGrant(t *testing.T) {
	now := time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	store := &memoryStore{grant: deliveryrecovery.Grant{
		ID: "local-1", ProviderGrantID: "asset-1",
		State: deliveryrecovery.StateRevokePending, NextRevokeAt: &due,
	}}
	revoker := &captureRevoker{}
	queue := work.Queue("commerce-recovery")
	definition := deliveryrecovery.ExtendWorkDefinition(work.Definition{
		Version: work.DefinitionVersion,
		Queues:  []work.QueueDefinition{{Key: queue, Concurrency: 1}},
	}, queue)
	catalog := work.MustCompile(definition)
	backend, err := work.NewMemory(catalog, work.MemoryOptions{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := work.NewRunner(
		catalog, backend,
		deliveryrecovery.WorkHandlers(store, backend, revoker, nil, func() time.Time { return now }),
		work.RunnerOptions{WorkerID: "test", Clock: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Enqueue(context.Background(), work.Request{
		Kind: deliveryrecovery.WorkKindScan, Payload: []byte(`{"limit":10}`),
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		worked, runErr := runner.RunOnce(context.Background(), queue, "test")
		if runErr != nil || !worked {
			t.Fatalf("run %d worked=%v err=%v", index, worked, runErr)
		}
	}
	if store.deferred != 1 || store.grant.State != deliveryrecovery.StateRevoked {
		t.Fatalf("store = %+v deferred=%d", store.grant, store.deferred)
	}
	if len(revoker.ids) != 1 || revoker.ids[0] != "asset-1" {
		t.Fatalf("revoked ids = %v", revoker.ids)
	}
}

func TestRevokeFailureRemainsRecoverable(t *testing.T) {
	store := &memoryStore{grant: deliveryrecovery.Grant{
		ID: "local-1", ProviderGrantID: "asset-1",
		State: deliveryrecovery.StateRevokePending, RevokeAttempts: 2,
	}}
	handler := deliveryrecovery.WorkHandlers(
		store, nil, &captureRevoker{err: errors.New("asset unavailable")}, nil, time.Now,
	)[deliveryrecovery.WorkKindRevoke]
	_, err := handler.Handle(context.Background(), work.Job{
		Payload: []byte(`{"grantId":"local-1"}`),
	}, nil)
	if err == nil {
		t.Fatal("expected retryable error")
	}
	if store.grant.RevokeAttempts != 3 ||
		store.grant.State != deliveryrecovery.StateRevokePending {
		t.Fatalf("grant = %+v", store.grant)
	}
}
