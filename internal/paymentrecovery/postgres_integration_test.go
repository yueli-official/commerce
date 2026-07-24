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
	assertReconciliationShape(t, database, true)
	assertRefundReconciliationShape(t, database, true)
	assertDisputeProjectionShape(t, database, true)
	assertAssetGrantRecoveryShape(t, database, true)
	assertAuditShape(t, database, true)

	applyMigration(t, database, "0014_audit_v1.down.sql", "down 0014")
	assertAuditShape(t, database, false)
	applyMigration(t, database, "0014_audit_v1.up.sql", "reapply 0014")
	assertAuditShape(t, database, true)

	applyMigration(
		t, database, "0013_asset_grant_recovery.down.sql", "down 0013",
	)
	assertAssetGrantRecoveryShape(t, database, false)
	applyMigration(
		t, database, "0013_asset_grant_recovery.up.sql", "reapply 0013",
	)
	assertAssetGrantRecoveryShape(t, database, true)

	applyMigration(
		t, database, "0012_dispute_projection.down.sql", "down 0012",
	)
	assertDisputeProjectionShape(t, database, false)
	applyMigration(
		t, database, "0012_dispute_projection.up.sql", "reapply 0012",
	)
	assertDisputeProjectionShape(t, database, true)

	applyMigration(
		t, database, "0011_refund_reconciliation.down.sql", "down 0011",
	)
	assertRefundReconciliationShape(t, database, false)
	applyMigration(
		t, database, "0011_refund_reconciliation.up.sql", "reapply 0011",
	)
	assertRefundReconciliationShape(t, database, true)

	applyMigration(
		t, database, "0010_payment_reconciliation.down.sql", "down 0010",
	)
	assertReconciliationShape(t, database, false)
	applyMigration(
		t, database, "0010_payment_reconciliation.up.sql", "reapply 0010",
	)
	assertReconciliationShape(t, database, true)

	applyMigration(
		t, database, "0012_dispute_projection.down.sql", "down 0012 before 0009",
	)
	applyMigration(t, database, "0009_payment_recovery.down.sql", "down 0009")
	assertRecoveryShape(t, database, false)

	applyMigration(t, database, "0009_payment_recovery.up.sql", "reapply 0009")
	applyMigration(
		t, database, "0010_payment_reconciliation.up.sql", "reapply 0010 after 0009",
	)
	applyMigration(
		t, database, "0011_refund_reconciliation.up.sql", "reapply 0011 after 0009",
	)
	applyMigration(
		t, database, "0012_dispute_projection.up.sql", "reapply 0012 after 0009",
	)
	assertRecoveryShape(t, database, true)
	assertReconciliationShape(t, database, true)
	assertRefundReconciliationShape(t, database, true)
	assertDisputeProjectionShape(t, database, true)
}

func applyMigration(t *testing.T, database *sql.DB, name, action string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "manifest", "sql", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(string(body)); err != nil {
		t.Fatalf("%s: %v", action, err)
	}
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

func assertReconciliationShape(t *testing.T, database *sql.DB, want bool) {
	t.Helper()
	for _, column := range []string{
		"last_reconciled_at", "next_reconcile_at",
		"reconciliation_failures", "reconciliation_error",
	} {
		var exists bool
		if err := database.QueryRow(`
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'payment_attempts'
      AND column_name = $1
)`, column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Fatalf("payment_attempts.%s exists=%v, want %v", column, exists, want)
		}
	}
}

func assertRefundReconciliationShape(t *testing.T, database *sql.DB, want bool) {
	t.Helper()
	for _, column := range []string{
		"last_reconciled_at", "next_reconcile_at",
		"reconciliation_failures", "reconciliation_error",
	} {
		var exists bool
		if err := database.QueryRow(`
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'refunds'
      AND column_name = $1
)`, column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Fatalf("refunds.%s exists=%v, want %v", column, exists, want)
		}
	}
}

func assertDisputeProjectionShape(t *testing.T, database *sql.DB, want bool) {
	t.Helper()
	for table, columns := range map[string][]string{
		"disputes": {
			"provider_tx_id", "provider_status", "outcome_code",
			"last_observed_at",
		},
		"entitlements":    {"suspended_at", "suspended_reason"},
		"delivery_grants": {"suspended_at", "suspended_reason"},
	} {
		for _, column := range columns {
			var exists bool
			if err := database.QueryRow(`
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = $1
      AND column_name = $2
)`, table, column).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != want {
				t.Fatalf("%s.%s exists=%v, want %v", table, column, exists, want)
			}
		}
	}
}

func assertAssetGrantRecoveryShape(t *testing.T, database *sql.DB, want bool) {
	t.Helper()
	var exists bool
	if err := database.QueryRow(`
SELECT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'asset_delivery_grants'
)`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != want {
		t.Fatalf("asset_delivery_grants exists=%v, want %v", exists, want)
	}
}

func assertAuditShape(t *testing.T, database *sql.DB, want bool) {
	t.Helper()
	for _, table := range []string{
		"audit_instances", "audit_events", "audit_event_receipts", "audit_mirror_outbox",
	} {
		var exists bool
		if err := database.QueryRow(`
SELECT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema() AND table_name = $1
)`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Fatalf("%s exists=%v, want %v", table, exists, want)
		}
	}
}

func randomSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
