package paymentrecovery_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

func TestPaymentRecoveryMigrationLifecycle(t *testing.T) {
	dsn := os.Getenv("COMMERCE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("COMMERCE_POSTGRES_DSN is not configured")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })

	schema := "commerce_recovery_test_" + time.Now().UTC().Format("20060102150405") +
		"_" + randomSuffix()
	if _, err := database.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
	})
	if _, err := database.Exec("SET search_path TO " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}

	migrations, err := filepath.Glob(filepath.Join("..", "..", "manifest", "sql", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(migrations)
	for _, path := range migrations {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(path), err)
		}
	}
	assertRecoveryShape(t, database, true)

	down, err := os.ReadFile(filepath.Join("..", "..", "manifest", "sql", "migrations", "0009_payment_recovery.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(string(down)); err != nil {
		t.Fatalf("down 0009: %v", err)
	}
	assertRecoveryShape(t, database, false)

	up, err := os.ReadFile(filepath.Join("..", "..", "manifest", "sql", "migrations", "0009_payment_recovery.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(string(up)); err != nil {
		t.Fatalf("reapply 0009: %v", err)
	}
	assertRecoveryShape(t, database, true)
}

func assertRecoveryShape(t *testing.T, database *sql.DB, want bool) {
	t.Helper()
	for _, table := range []string{"payment_attempts", "provider_events", "refunds", "disputes"} {
		var exists bool
		if err := database.QueryRow(`
SELECT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema() AND table_name = $1
)`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Fatalf("table %s exists=%v, want %v", table, exists, want)
		}
	}
	for _, column := range []string{"payment_state", "refunded_amount_cents", "dispute_state"} {
		var exists bool
		if err := database.QueryRow(`
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema() AND table_name = 'orders' AND column_name = $1
)`, column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Fatalf("orders.%s exists=%v, want %v", column, exists, want)
		}
	}
}

func randomSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
