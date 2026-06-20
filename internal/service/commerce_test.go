// Package service_test exercises the commerce Service against a live PostgreSQL instance.
//
// Run with COMMERCE_PG_HOST set; the test will skip if the env var is absent:
//
//	COMMERCE_PG_HOST=192.168.5.5 COMMERCE_PG_USER=postgres COMMERCE_PG_PASS=postgres \
//	  go test -p 1 ./internal/service/...
package service_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gogf/gf/v2/database/gdb"
	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/service"
)

// ----- test helpers --------------------------------------------------------

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// newDB opens a GoFrame gdb.DB connection against the commerce database.
// It skips the test if COMMERCE_PG_HOST is not set.
func newDB(t *testing.T) gdb.DB {
	t.Helper()
	host := os.Getenv("COMMERCE_PG_HOST")
	if host == "" {
		t.Skip("set COMMERCE_PG_HOST/USER/PASS to run service integration tests")
	}
	port := envOr("COMMERCE_PG_PORT", "5432")
	user := envOr("COMMERCE_PG_USER", "postgres")
	pass := os.Getenv("COMMERCE_PG_PASS")

	// GoFrame link format for pgsql: pgsql:user:pass@tcp(host:port)/dbname
	link := fmt.Sprintf("pgsql:%s:%s@tcp(%s:%s)/commerce", user, pass, host, port)
	db, err := gdb.New(gdb.ConfigNode{Link: link})
	if err != nil {
		t.Fatalf("gdb.New: %v", err)
	}
	return db
}

// resetSchema drops and re-creates the three commerce tables using 0001_init.up.sql.
func resetSchema(t *testing.T, db gdb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS entitlements",
		"DROP TABLE IF EXISTS orders",
		"DROP TABLE IF EXISTS products",
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("drop table: %v", err)
		}
	}
	up, err := os.ReadFile("../../manifest/sql/migrations/0001_init.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}

// newSvc returns a fresh Service, its DAO, and a background context.
// The schema is wiped and re-applied each call.
func newSvc(t *testing.T) (*service.Service, *dao.PG, context.Context) {
	t.Helper()
	db := newDB(t)
	resetSchema(t, db)
	pg := dao.NewPG(db)
	svc := service.New(pg)
	return svc, pg, context.Background()
}

// uid generates a test-run-unique string to avoid cross-test row collisions.
func uid(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, os.Getpid())
}

// isCoded reports whether err carries the given errs code string.
func isCoded(err error, code string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), code)
}

// isAlnum reports whether r is ASCII alphanumeric.
func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// ----- tests ----------------------------------------------------------------

// TestCreateOrder_LazyUpsertProduct verifies that CreateOrder lazily creates the product
// and returns both order + product.  A second call with the same (site_key, external_id)
// upserts the product rather than inserting a duplicate.
func TestCreateOrder_LazyUpsertProduct(t *testing.T) {
	svc, _, ctx := newSvc(t)
	sub := uid("sub-lazy")
	desc := service.OrderDesc{
		SiteKey:    "resource",
		ExternalID: uid("ext-lazy"),
		Kind:       "paid",
		Title:      "My Post",
		Currency:   "CNY",
		PriceCents: 1000,
	}

	o, p, err := svc.CreateOrder(ctx, sub, desc)
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if o == nil || p == nil {
		t.Fatal("CreateOrder returned nil order or product")
	}
	if o.Status != model.OrderStatusPending {
		t.Errorf("order status = %q, want pending", o.Status)
	}
	if p.PriceCents != 1000 {
		t.Errorf("product price_cents = %d, want 1000", p.PriceCents)
	}
	productID := p.ID

	// Second order with updated price — same UNIQUE (site_key, external_id) → upsert.
	desc2 := desc
	desc2.PriceCents = 2000
	desc2.Title = "My Post v2"
	_, p2, err := svc.CreateOrder(ctx, sub, desc2)
	if err != nil {
		t.Fatalf("CreateOrder (upsert): %v", err)
	}
	if p2.ID != productID {
		t.Errorf("upserted product ID changed: got %q, want %q", p2.ID, productID)
	}
	if p2.PriceCents != 2000 {
		t.Errorf("upserted price_cents = %d, want 2000", p2.PriceCents)
	}
}

// TestSetPaying_ValidTransition verifies pending→paying succeeds.
func TestSetPaying_ValidTransition(t *testing.T) {
	svc, _, ctx := newSvc(t)
	o, _, err := svc.CreateOrder(ctx, uid("sub-sp1"), service.OrderDesc{
		SiteKey: "resource", ExternalID: uid("ext-sp1"),
		Kind: "paid", Title: "T", Currency: "CNY", PriceCents: 500,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := svc.SetPaying(ctx, o.OrderNo); err != nil {
		t.Fatalf("SetPaying: %v", err)
	}
}

// TestSetPaying_NonPendingOrder verifies that calling SetPaying on a paying order
// returns commerce.order_invalid_state.
func TestSetPaying_NonPendingOrder(t *testing.T) {
	svc, _, ctx := newSvc(t)
	o, _, err := svc.CreateOrder(ctx, uid("sub-sp2"), service.OrderDesc{
		SiteKey: "resource", ExternalID: uid("ext-sp2"),
		Kind: "paid", Title: "T", Currency: "CNY", PriceCents: 500,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := svc.SetPaying(ctx, o.OrderNo); err != nil {
		t.Fatalf("SetPaying (first): %v", err)
	}
	// Second SetPaying on same order (already paying) must fail.
	err = svc.SetPaying(ctx, o.OrderNo)
	if !isCoded(err, commerceerr.CodeOrderInvalidState) {
		t.Errorf("SetPaying on paying order: want order_invalid_state, got %v", err)
	}
}

// TestMarkPaid_IllegalTransition verifies pending→paid (skipping paying) is rejected.
func TestMarkPaid_IllegalTransition(t *testing.T) {
	svc, _, ctx := newSvc(t)
	o, _, err := svc.CreateOrder(ctx, uid("sub-mp-ill"), service.OrderDesc{
		SiteKey: "resource", ExternalID: uid("ext-mp-ill"),
		Kind: "paid", Title: "T", Currency: "CNY", PriceCents: 800,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	// Order is still pending — MarkPaid must reject it.
	err = svc.MarkPaid(ctx, o.OrderNo, "tx-ill", 800)
	if !isCoded(err, commerceerr.CodeOrderInvalidState) {
		t.Errorf("MarkPaid on pending order: want order_invalid_state, got %v", err)
	}
}

// TestMarkPaid_AmountMismatch verifies that wrong amount returns commerce.notify_invalid.
func TestMarkPaid_AmountMismatch(t *testing.T) {
	svc, _, ctx := newSvc(t)
	o, _, err := svc.CreateOrder(ctx, uid("sub-amt"), service.OrderDesc{
		SiteKey: "resource", ExternalID: uid("ext-amt"),
		Kind: "paid", Title: "T", Currency: "CNY", PriceCents: 999,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := svc.SetPaying(ctx, o.OrderNo); err != nil {
		t.Fatalf("SetPaying: %v", err)
	}
	err = svc.MarkPaid(ctx, o.OrderNo, "tx-bad", 1)
	if !isCoded(err, commerceerr.CodeNotifyInvalid) {
		t.Errorf("MarkPaid with bad amount: want notify_invalid, got %v", err)
	}
}

// TestMarkPaid_Idempotent verifies that calling MarkPaid twice on the same order
// grants the entitlement exactly once (ON CONFLICT DO NOTHING prevents double-grant).
func TestMarkPaid_Idempotent(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	sub := uid("sub-idem")
	o, p, err := svc.CreateOrder(ctx, sub, service.OrderDesc{
		SiteKey: "resource", ExternalID: uid("ext-idem"),
		Kind: "paid", Title: "T", Currency: "CNY", PriceCents: 1200,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := svc.SetPaying(ctx, o.OrderNo); err != nil {
		t.Fatalf("SetPaying: %v", err)
	}

	// First MarkPaid → should succeed and insert entitlement.
	if err := svc.MarkPaid(ctx, o.OrderNo, "tx-idem", 1200); err != nil {
		t.Fatalf("MarkPaid (first): %v", err)
	}
	exists, err := pg.EntitlementExists(ctx, sub, p.ID)
	if err != nil {
		t.Fatalf("EntitlementExists: %v", err)
	}
	if !exists {
		t.Fatal("entitlement not found after first MarkPaid")
	}

	// Second MarkPaid on already-paid order → idempotent nil, no second entitlement.
	if err := svc.MarkPaid(ctx, o.OrderNo, "tx-idem-dup", 1200); err != nil {
		t.Fatalf("MarkPaid (second, idempotent): %v", err)
	}
	// Assert count is exactly 1 — independent of ON CONFLICT, this locks in "no second grant".
	count, err := pg.EntitlementCount(ctx, sub, p.ID)
	if err != nil {
		t.Fatalf("EntitlementCount (after second): %v", err)
	}
	if count != 1 {
		t.Errorf("entitlement count = %d after two MarkPaid calls, want 1", count)
	}
}

// TestEntitled_NotPurchased_ProductAbsent verifies {false, not_purchased, PriceCents=nil}
// when the product has never been created.
func TestEntitled_NotPurchased_ProductAbsent(t *testing.T) {
	svc, _, ctx := newSvc(t)
	res, err := svc.Entitled(ctx, "any-sub", "resource", uid("nonexistent"))
	if err != nil {
		t.Fatalf("Entitled: %v", err)
	}
	if res.Entitled {
		t.Error("want Entitled=false for absent product")
	}
	if res.Reason != "not_purchased" {
		t.Errorf("reason = %q, want not_purchased", res.Reason)
	}
	if res.Required.PriceCents != nil {
		t.Errorf("Required.PriceCents = %v, want nil for absent product", res.Required.PriceCents)
	}
}

// TestEntitled_NotPurchased_ProductExists verifies {false, not_purchased, PriceCents!=nil}
// when the product exists but this sub has no entitlement.
func TestEntitled_NotPurchased_ProductExists(t *testing.T) {
	svc, _, ctx := newSvc(t)
	extID := uid("ext-notbought")
	// Create the product via CreateOrder (different sub).
	_, _, err := svc.CreateOrder(ctx, uid("sub-seller"), service.OrderDesc{
		SiteKey: "resource", ExternalID: extID,
		Kind: "paid", Title: "T", Currency: "CNY", PriceCents: 500,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	// A different sub that has NOT purchased.
	res, err := svc.Entitled(ctx, uid("sub-buyer"), "resource", extID)
	if err != nil {
		t.Fatalf("Entitled: %v", err)
	}
	if res.Entitled {
		t.Error("want Entitled=false for non-purchaser")
	}
	if res.Reason != "not_purchased" {
		t.Errorf("reason = %q, want not_purchased", res.Reason)
	}
	if res.Required.PriceCents == nil || *res.Required.PriceCents != 500 {
		t.Errorf("Required.PriceCents = %v, want &500", res.Required.PriceCents)
	}
}

// TestEntitled_OK verifies that after a successful paid order, Entitled returns {true, ok}.
func TestEntitled_OK(t *testing.T) {
	svc, _, ctx := newSvc(t)
	sub := uid("sub-ok")
	extID := uid("ext-ok")
	o, _, err := svc.CreateOrder(ctx, sub, service.OrderDesc{
		SiteKey: "resource", ExternalID: extID,
		Kind: "paid", Title: "T", Currency: "CNY", PriceCents: 700,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := svc.SetPaying(ctx, o.OrderNo); err != nil {
		t.Fatalf("SetPaying: %v", err)
	}
	if err := svc.MarkPaid(ctx, o.OrderNo, "tx-ok", 700); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}

	res, err := svc.Entitled(ctx, sub, "resource", extID)
	if err != nil {
		t.Fatalf("Entitled: %v", err)
	}
	if !res.Entitled {
		t.Error("want Entitled=true after paid order")
	}
	if res.Reason != "ok" {
		t.Errorf("reason = %q, want ok", res.Reason)
	}
}

// TestOrderNo_Format verifies NewOrderNo produces valid Alipay out_trade_no values.
func TestOrderNo_Format(t *testing.T) {
	for i := 0; i < 100; i++ {
		no := service.NewOrderNo()
		if len(no) > 64 {
			t.Errorf("NewOrderNo() length %d > 64: %q", len(no), no)
		}
		if !strings.HasPrefix(no, "CM") {
			t.Errorf("NewOrderNo() missing CM prefix: %q", no)
		}
		for _, ch := range no {
			if !isAlnum(ch) {
				t.Errorf("NewOrderNo() contains non-alnum char %q in %q", ch, no)
				break
			}
		}
	}
}
