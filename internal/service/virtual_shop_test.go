package service_test

import (
	"context"
	"testing"

	"github.com/gogf/gf/v2/database/gdb"

	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/service"
)

func assertTable(t *testing.T, db gdb.DB, table string) {
	t.Helper()
	v, err := db.GetValue(context.Background(), `
SELECT EXISTS (
  SELECT 1 FROM information_schema.tables
  WHERE table_schema = 'public' AND table_name = ?
)`, table)
	if err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	if !v.Bool() {
		t.Fatalf("expected table %s to exist", table)
	}
}

func assertColumn(t *testing.T, db gdb.DB, table, column string) {
	t.Helper()
	v, err := db.GetValue(context.Background(), `
SELECT EXISTS (
  SELECT 1 FROM information_schema.columns
  WHERE table_schema = 'public' AND table_name = ? AND column_name = ?
)`, table, column)
	if err != nil {
		t.Fatalf("check column %s.%s: %v", table, column, err)
	}
	if !v.Bool() {
		t.Fatalf("expected column %s.%s to exist", table, column)
	}
}

func TestVirtualShopMigrationShape(t *testing.T) {
	db := newDB(t)
	resetSchema(t, db)

	assertTable(t, db, "commerce_buyers")
	assertTable(t, db, "order_items")
	assertTable(t, db, "payment_events")
	assertTable(t, db, "delivery_grants")
	assertColumn(t, db, "orders", "buyer_email")
	assertColumn(t, db, "orders", "buyer_sub")
	assertColumn(t, db, "orders", "delivery_state")
}

func TestGuestCheckoutCreatesBuyerOrderItemAndGrantOnSettle(t *testing.T) {
	svc, _, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerEmail: " Buyer@Example.COM ",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   uid("variant-guest"),
			VariantID:    uid("variant-id-guest"),
			Title:        "Icon Pack",
			VariantTitle: "Personal License",
			SKU:          "ICON-PERSONAL",
			PriceCents:   1900,
			Currency:     "CNY",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-123",
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if order.BuyerEmail != "buyer@example.com" {
		t.Fatalf("buyer email = %q, want normalized buyer@example.com", order.BuyerEmail)
	}
	if order.Status != model.OrderStatusPaying {
		t.Fatalf("status = %q, want paying", order.Status)
	}

	grant, err := svc.SettleCheckout(ctx, order.OrderNo, "dev", "dev-tx", 1900)
	if err != nil {
		t.Fatalf("SettleCheckout: %v", err)
	}
	if grant.Token == "" {
		t.Fatal("expected one-time guest delivery token")
	}
	if grant.State != model.DeliveryStateGranted {
		t.Fatalf("grant state = %q, want granted", grant.State)
	}
	if grant.DeliveryRef != "asset-123" {
		t.Fatalf("delivery ref = %q, want asset-123", grant.DeliveryRef)
	}
}

func TestUserCheckoutGrantsEntitlementOnSettle(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	sub := uid("buyer-sub")
	externalID := uid("variant-user")
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub:   sub,
		BuyerEmail: "user@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   externalID,
			VariantID:    uid("variant-id-user"),
			Title:        "Template Pack",
			VariantTitle: "Commercial License",
			SKU:          "TPL-COMM",
			PriceCents:   3900,
			Currency:     "CNY",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-456",
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if _, err := svc.SettleCheckout(ctx, order.OrderNo, "dev", "dev-tx-user", 3900); err != nil {
		t.Fatalf("SettleCheckout: %v", err)
	}
	p, err := pg.GetProductByExternal(ctx, "shop", externalID)
	if err != nil {
		t.Fatalf("GetProductByExternal: %v", err)
	}
	if p == nil {
		t.Fatal("product not found")
	}
	ok, err := pg.EntitlementExists(ctx, sub, p.ID)
	if err != nil {
		t.Fatalf("EntitlementExists: %v", err)
	}
	if !ok {
		t.Fatal("expected entitlement for logged-in buyer")
	}
}
