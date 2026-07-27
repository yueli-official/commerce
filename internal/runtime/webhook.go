package runtime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/yueli-official/foundation/go/webhook"
	"github.com/yueli-official/foundation/go/webhook/workadapter"
	"github.com/yueli-official/foundation/go/work"
	workpostgres "github.com/yueli-official/foundation/go/work/postgres"
)

func DecodeWebhookMasterKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("commerce webhook: master key is required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, fmt.Errorf("commerce webhook: master key must be 32 bytes encoded as base64 or hex")
}

type WebhookOptions struct {
	DB          *sql.DB
	InstanceKey string
	Definition  webhook.Definition
	MasterKey   []byte
	WorkerID    string
	Concurrency int
	Clock       func() time.Time
	OnError     func(error)
}

type WebhookRuntime struct {
	Hooks       *webhook.Postgres
	Work        *workpostgres.Adapter
	WorkCatalog *work.Catalog
	Runner      *work.Runner
}

func NewWebhook(ctx context.Context, options WebhookOptions) (*WebhookRuntime, error) {
	if options.DB == nil {
		return nil, fmt.Errorf("commerce webhook: database is required")
	}
	options.InstanceKey = strings.TrimSpace(options.InstanceKey)
	if options.InstanceKey == "" {
		return nil, fmt.Errorf("commerce webhook: instance key is required")
	}
	if options.Concurrency == 0 {
		options.Concurrency = 4
	}
	if options.Concurrency < 1 || options.Concurrency > 64 {
		return nil, fmt.Errorf("commerce webhook: concurrency must be between 1 and 64")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if strings.TrimSpace(options.WorkerID) == "" {
		options.WorkerID = options.InstanceKey + ":worker"
	}
	catalog, err := webhook.Compile(options.Definition)
	if err != nil {
		return nil, err
	}
	workCatalog, err := work.Compile(work.Definition{
		Version: work.DefinitionVersion,
		Queues:  []work.QueueDefinition{{Key: "webhook", Concurrency: options.Concurrency}},
		Kinds:   []work.KindDefinition{webhook.WorkDefinition("webhook")},
	})
	if err != nil {
		return nil, err
	}
	workAdapter, err := workpostgres.New(ctx, workCatalog, workpostgres.Options{
		DB: options.DB, InstanceKey: options.InstanceKey + ":work", Clock: options.Clock,
	})
	if err != nil {
		return nil, err
	}
	secretStore, err := webhook.NewPostgresSecretStore(
		options.DB, options.InstanceKey, options.MasterKey, options.Clock,
	)
	if err != nil {
		return nil, err
	}
	scheduler := &workadapter.Adapter{Work: workAdapter}
	hooks, err := webhook.NewPostgres(ctx, catalog, webhook.PostgresOptions{
		DB: options.DB, InstanceKey: options.InstanceKey, Clock: options.Clock,
		Scheduler: scheduler, Secrets: secretStore,
	})
	if err != nil {
		return nil, err
	}
	driver := &webhook.DeliveryDriver{
		Backend: hooks, Secrets: secretStore,
		Authorizer: webhook.NetworkAuthorizer{Policy: webhook.PublicNetworkPolicy(), Clock: options.Clock},
		Sender:     webhook.HTTPSender{Clock: options.Clock},
		Retry:      catalog.Retry(), Limits: catalog.Limits(), Clock: options.Clock,
	}
	runner, err := work.NewRunner(
		workCatalog, workAdapter,
		map[work.Kind]work.Handler{webhook.WorkKind: webhook.NewWorkHandler(driver)},
		work.RunnerOptions{
			WorkerID: options.WorkerID, PollInterval: time.Second, Clock: options.Clock,
			OnError: options.OnError,
		},
	)
	if err != nil {
		return nil, err
	}
	return &WebhookRuntime{Hooks: hooks, Work: workAdapter, WorkCatalog: workCatalog, Runner: runner}, nil
}
