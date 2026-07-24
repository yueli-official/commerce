// Package commerceaudit defines the minimum durable governance evidence for
// Commerce recovery and access-revocation actions.
package commerceaudit

import (
	"context"
	"database/sql"
	"time"

	"github.com/yueli-official/foundation/go/audit"
	foundationauth "github.com/yueli-official/foundation/go/auth"
	"github.com/yueli-official/foundation/go/authorization"
	"go.opentelemetry.io/otel/trace"
)

type Action string

const (
	ActionAccessRevocationQueued Action = "commerce.delivery.revocation_queued"
	ActionRemoteGrantRevoked     Action = "commerce.delivery.remote_revoked"
	ActionRecoveryRetried        Action = "commerce.recovery.retried"
)

type Evidence struct {
	State           string
	ProviderGrantID string
	Count           uint64
	Attempts        uint64
}

type Journal struct {
	core      *audit.Postgres
	contracts map[Action]audit.Contract[Evidence]
}

func New(ctx context.Context, db *sql.DB, instance string) (*Journal, error) {
	catalog, err := audit.Compile(Definition())
	if err != nil {
		return nil, err
	}
	core, err := audit.NewPostgres(ctx, catalog, audit.PostgresOptions{
		DB: db, InstanceKey: "commerce:" + instance,
		Source: audit.Source{
			Service: "commerce-api", Module: "commerce", Instance: instance,
		},
		EnableMirrorOutbox: true,
	})
	if err != nil {
		return nil, err
	}
	journal := &Journal{core: core, contracts: make(map[Action]audit.Contract[Evidence])}
	for _, action := range actions() {
		contract, err := audit.BindAction(
			catalog, audit.Action{Name: audit.ActionName(action), Version: 1}, encodeEvidence,
		)
		if err != nil {
			return nil, err
		}
		journal.contracts[action] = contract
	}
	return journal, nil
}

func Definition() audit.Definition {
	definitions := make([]audit.ActionDefinition, 0, len(actions()))
	for _, action := range actions() {
		definitions = append(definitions, audit.ActionDefinition{
			Action:   audit.Action{Name: audit.ActionName(action), Version: 1},
			Category: audit.CategoryAdministration,
			TargetTypes: []string{
				"commerce.order", "commerce.delivery_grant",
				"commerce.payment_attempt", "commerce.refund",
			},
			Commit: audit.CommitAtomicRequired, Retention: "retention.commerce_recovery",
			Evidence: []audit.FieldDefinition{
				{Key: "commerce.recovery_state", Kind: audit.EvidenceCode},
				{Key: "commerce.provider_grant", Kind: audit.EvidenceReference},
				{Key: "commerce.affected_count", Kind: audit.EvidenceCount},
				{Key: "commerce.recovery_attempts", Kind: audit.EvidenceCount},
			},
		})
	}
	return audit.Definition{
		Version: 1, Consumer: "commerce.audit", MaxBatch: 100, MaxEvidence: 4,
		Retention: []audit.RetentionDefinition{{
			Class:      "retention.commerce_recovery",
			MinimumAge: 365 * 24 * time.Hour, ArchiveBefore: true,
		}},
		Actions: definitions,
	}
}

func (journal *Journal) Hook(
	ctx context.Context,
	action Action,
	eventID string,
	target audit.Target,
	evidence Evidence,
) func(context.Context, *sql.Tx) error {
	if journal == nil {
		return nil
	}
	actor := actorFromContext(ctx)
	correlation := correlationFromContext(ctx)
	occurredAt := time.Now().UTC()
	return func(txCtx context.Context, tx *sql.Tx) error {
		appender, err := journal.core.Bind(tx)
		if err != nil {
			return err
		}
		_, err = audit.Record(txCtx, appender, journal.contracts[action], audit.Attempt[Evidence]{
			ID: audit.EventID(eventID), Actor: actor, Target: target,
			Outcome:     audit.Outcome{Kind: audit.OutcomeSucceeded},
			Correlation: correlation, OccurredAt: occurredAt, Evidence: evidence,
		})
		return err
	}
}

func (journal *Journal) Reader() audit.Reader {
	if journal == nil {
		return nil
	}
	return journal.core
}

func actions() []Action {
	return []Action{
		ActionAccessRevocationQueued,
		ActionRemoteGrantRevoked,
		ActionRecoveryRetried,
	}
}

func encodeEvidence(value Evidence) []audit.EvidenceField {
	fields := []audit.EvidenceField{
		audit.Code("commerce.recovery_state", value.State),
		audit.Count("commerce.affected_count", value.Count),
		audit.Count("commerce.recovery_attempts", value.Attempts),
	}
	if value.ProviderGrantID != "" {
		fields = append(fields, audit.Reference("commerce.provider_grant", value.ProviderGrantID))
	}
	return fields
}

func actorFromContext(ctx context.Context) audit.Actor {
	if principal, ok := foundationauth.FromContext(ctx); ok {
		if principal.Subject != "" {
			return audit.Actor{Kind: audit.ActorUser, ID: principal.Subject}
		}
		if principal.ClientID != "" {
			return audit.Actor{Kind: audit.ActorService, ID: principal.ClientID}
		}
	}
	return audit.Actor{Kind: audit.ActorSystem, ID: "commerce-api"}
}

func correlationFromContext(ctx context.Context) audit.Correlation {
	value := audit.Correlation{
		RequestID: authorization.RequestMetadataFromContext(ctx).CorrelationID,
	}
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		value.TraceID = span.TraceID().String()
		value.SpanID = span.SpanID().String()
	}
	return value
}
