package commerceaudit_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
	_ "github.com/lib/pq"
	"github.com/yueli-official/foundation/go/audit"

	"github.com/yueli-official/commerce/internal/commerceaudit"
)

func TestPostgresHookSharesCallerTransaction(t *testing.T) {
	database := openAuditPostgres(t)
	journal, err := commerceaudit.New(context.Background(), database, "integration")
	if err != nil {
		t.Fatal(err)
	}
	target := audit.Target{Type: "commerce.delivery_grant", ID: "grant-1"}
	evidence := commerceaudit.Evidence{
		State: "revoke_pending", ProviderGrantID: "asset-grant-1", Count: 1,
	}
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	hook := journal.Hook(
		context.Background(), commerceaudit.ActionAccessRevocationQueued,
		"event-rollback", target, evidence,
	)
	if err := hook(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	page, err := journal.Reader().Query(context.Background(), audit.Query{})
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("rolled back audit = %#v, %v", page, err)
	}

	tx, err = database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	hook = journal.Hook(
		context.Background(), commerceaudit.ActionAccessRevocationQueued,
		"event-commit", target, evidence,
	)
	if err := hook(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err = journal.Reader().Query(context.Background(), audit.Query{})
	if err != nil || len(page.Events) != 1 || page.Events[0].Target.ID != target.ID {
		t.Fatalf("committed audit = %#v, %v", page, err)
	}
}

func openAuditPostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("AUDIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AUDIT_POSTGRES_DSN is not configured")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	schema := fmt.Sprintf("commerce_audit_test_%d", time.Now().UnixNano())
	if _, err := database.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("SET search_path TO " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(audit.PostgresSchemaUp()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(audit.PostgresSchemaDown())
		_, _ = database.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema))
		_ = database.Close()
	})
	return database
}
