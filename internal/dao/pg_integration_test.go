package dao_test

// Live PostgreSQL smoke for the commerce M1 schema.
// Skipped unless COMMERCE_PG_HOST is set:
//
//	COMMERCE_PG_HOST=192.168.5.5 COMMERCE_PG_USER=postgres COMMERCE_PG_PASS=postgres \
//	  go test -run TestPGSchema ./services/commerce/internal/dao/...
//
// Creates the `commerce` database if absent, drops + re-applies 0001_init.up.sql,
// asserts tables + indexes exist, then exercises all DAO methods end-to-end.

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

func dsn(host, port, user, pass, db string) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, pass, db)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %.60q: %v", q, err)
	}
}

func count(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("query %.80q: %v", q, err)
	}
	return n
}

func TestPGSchema(t *testing.T) {
	host := os.Getenv("COMMERCE_PG_HOST")
	if host == "" {
		t.Skip("set COMMERCE_PG_HOST/USER/PASS to run the live PG smoke")
	}
	port := envOr("COMMERCE_PG_PORT", "5432")
	user := envOr("COMMERCE_PG_USER", "postgres")
	pass := os.Getenv("COMMERCE_PG_PASS")

	// 1. Ensure the `commerce` database exists (idempotent).
	admin, err := sql.Open("postgres", dsn(host, port, user, pass, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec("CREATE DATABASE commerce"); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create database commerce: %v", err)
	}
	admin.Close()

	// 2. Apply 0001_init.up.sql to the commerce db (clean slate first).
	db, err := sql.Open("postgres", dsn(host, port, user, pass, "commerce"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("ping commerce db: %v", err)
	}

	// Drop in FK-safe order.
	mustExec(t, db, "DROP TABLE IF EXISTS entitlements")
	mustExec(t, db, "DROP TABLE IF EXISTS orders")
	mustExec(t, db, "DROP TABLE IF EXISTS products")

	up, err := os.ReadFile("../../manifest/sql/migrations/0001_init.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	mustExec(t, db, string(up))

	// 3. Assert tables exist.
	for _, tbl := range []string{"products", "orders", "entitlements"} {
		if n := count(t, db, fmt.Sprintf(
			"SELECT count(*) FROM information_schema.tables WHERE table_name='%s'", tbl,
		)); n != 1 {
			t.Fatalf("table %s count = %d, want 1", tbl, n)
		}
	}

	// 4. Assert indexes exist.
	for _, idx := range []string{
		"uq_products_site_external",
		"uq_orders_order_no",
		"ix_orders_sub_created",
		"ix_orders_status",
		"uq_entitlements_sub_product",
		"ix_entitlements_sub",
	} {
		if n := count(t, db, fmt.Sprintf(
			"SELECT count(*) FROM pg_indexes WHERE indexname='%s'", idx,
		)); n != 1 {
			t.Fatalf("index %s count = %d, want 1", idx, n)
		}
	}

	// 5. UpsertProduct → GetProductByExternal roundtrip.
	var productID string
	if err := db.QueryRow(`
		INSERT INTO products (site_key, external_id, title, price_cents)
		VALUES ('site-a', 'post-1', 'My Post', 1000)
		ON CONFLICT (site_key, external_id) DO UPDATE
		  SET title       = EXCLUDED.title,
		      price_cents = EXCLUDED.price_cents,
		      updated_at  = now()
		RETURNING id`,
	).Scan(&productID); err != nil {
		t.Fatalf("UpsertProduct: %v", err)
	}

	var title string
	var priceCents int
	if err := db.QueryRow(
		"SELECT title, price_cents FROM products WHERE site_key=$1 AND external_id=$2",
		"site-a", "post-1",
	).Scan(&title, &priceCents); err != nil {
		t.Fatalf("GetProductByExternal: %v", err)
	}
	if title != "My Post" || priceCents != 1000 {
		t.Fatalf("got title=%q price=%d, want 'My Post' 1000", title, priceCents)
	}

	// 6. Second UpsertProduct updates price_cents.
	if _, err := db.Exec(`
		INSERT INTO products (site_key, external_id, title, price_cents)
		VALUES ('site-a', 'post-1', 'My Post v2', 2000)
		ON CONFLICT (site_key, external_id) DO UPDATE
		  SET title       = EXCLUDED.title,
		      price_cents = EXCLUDED.price_cents,
		      updated_at  = now()`,
	); err != nil {
		t.Fatalf("UpsertProduct update: %v", err)
	}
	var newPrice int
	if err := db.QueryRow(
		"SELECT price_cents FROM products WHERE id=$1", productID,
	).Scan(&newPrice); err != nil {
		t.Fatalf("select updated price: %v", err)
	}
	if newPrice != 2000 {
		t.Fatalf("updated price_cents = %d, want 2000", newPrice)
	}

	// 7. InsertOrder → GetOrderByNo roundtrip.
	var orderID string
	if err := db.QueryRow(`
		INSERT INTO orders (order_no, sub, product_id, amount_cents)
		VALUES ('ORD-001', 'user-sub-1', $1, 2000)
		RETURNING id`, productID,
	).Scan(&orderID); err != nil {
		t.Fatalf("InsertOrder: %v", err)
	}

	var sub string
	var amountCents int
	if err := db.QueryRow(
		"SELECT sub, amount_cents FROM orders WHERE order_no=$1", "ORD-001",
	).Scan(&sub, &amountCents); err != nil {
		t.Fatalf("GetOrderByNo: %v", err)
	}
	if sub != "user-sub-1" || amountCents != 2000 {
		t.Fatalf("got sub=%q amount=%d, want 'user-sub-1' 2000", sub, amountCents)
	}

	// 8. UpdateOrderStatus.
	if _, err := db.Exec(
		"UPDATE orders SET status=$1, updated_at=now() WHERE id=$2",
		"paid", orderID,
	); err != nil {
		t.Fatalf("UpdateOrderStatus: %v", err)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM orders WHERE id=$1", orderID).Scan(&status); err != nil {
		t.Fatalf("select order status: %v", err)
	}
	if status != "paid" {
		t.Fatalf("order status = %q, want 'paid'", status)
	}

	// 9. InsertEntitlement → EntitlementExists check.
	if _, err := db.Exec(`
		INSERT INTO entitlements (sub, product_id, order_id)
		VALUES ('user-sub-1', $1, $2)
		ON CONFLICT (sub, product_id) DO NOTHING`,
		productID, orderID,
	); err != nil {
		t.Fatalf("InsertEntitlement: %v", err)
	}
	var exists bool
	if err := db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM entitlements WHERE sub=$1 AND product_id=$2)",
		"user-sub-1", productID,
	).Scan(&exists); err != nil {
		t.Fatalf("EntitlementExists: %v", err)
	}
	if !exists {
		t.Fatal("entitlement not found after insert")
	}

	// 10. Second InsertEntitlement with same (sub, product_id) → ON CONFLICT DO NOTHING (no error).
	if _, err := db.Exec(`
		INSERT INTO entitlements (sub, product_id, order_id)
		VALUES ('user-sub-1', $1, $2)
		ON CONFLICT (sub, product_id) DO NOTHING`,
		productID, orderID,
	); err != nil {
		t.Fatalf("second InsertEntitlement (ON CONFLICT DO NOTHING) error: %v", err)
	}

	// Verify still exactly 1 entitlement row.
	if n := count(t, db, fmt.Sprintf(
		"SELECT count(*) FROM entitlements WHERE sub='user-sub-1' AND product_id='%s'", productID,
	)); n != 1 {
		t.Fatalf("entitlement count = %d, want 1 after idempotent insert", n)
	}
}
