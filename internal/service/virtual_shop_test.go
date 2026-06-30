package service_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

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
	mailer := &captureDeliveryMailer{}
	svc.ConfigureDelivery(service.DeliveryConfig{
		SigningSecret: "test-secret",
		PublicBaseURL: "https://shop.example",
		TTL:           time.Minute,
	})
	svc.ConfigureDeliveryMailer(mailer)
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
	if len(mailer.mails) != 1 {
		t.Fatalf("delivery mails = %d, want 1", len(mailer.mails))
	}
	mail := mailer.mails[0]
	if mail.To != "buyer@example.com" {
		t.Fatalf("mail to = %q, want buyer@example.com", mail.To)
	}
	if mail.OrderNo != order.OrderNo || mail.DeliveryRef != "asset-123" {
		t.Fatalf("unexpected mail payload: %+v", mail)
	}
	if !strings.Contains(mail.DeliveryURL, "/api/v1/delivery/") || !strings.Contains(mail.DeliveryURL, "sig=") {
		t.Fatalf("mail delivery url missing signed handoff: %q", mail.DeliveryURL)
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

func TestPointsCheckoutSpendsCreditsAndCreatesDelivery(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	svc.ConfigureDelivery(service.DeliveryConfig{
		SigningSecret: "test-secret",
		PublicBaseURL: "https://shop.example",
		TTL:           time.Minute,
	})
	assetDelivery := &captureAssetDelivery{url: "https://asset.example/grants/download-token"}
	svc.ConfigureAssetDeliveryClient(assetDelivery)
	sub := uid("points-buyer")
	if err := pg.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		return pg.EarnCreditsTx(ctx, tx, sub, 100, model.CreditsSourceGrant, "test")
	}); err != nil {
		t.Fatalf("seed credits: %v", err)
	}
	externalID := uid("variant-points")
	res, err := svc.RedeemCheckout(ctx, service.CheckoutDesc{
		BuyerSub:   sub,
		BuyerEmail: "points@example.com",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   externalID,
			VariantID:    uid("variant-id-points"),
			Title:        "Points Template",
			VariantTitle: "Points License",
			SKU:          "PTS-TPL",
			PriceCents:   0,
			PointsCost:   30,
			Currency:     "POINTS",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-points",
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("RedeemCheckout: %v", err)
	}
	if res.Balance != 70 {
		t.Fatalf("balance = %d, want 70", res.Balance)
	}
	if res.Grant.Token == "" {
		t.Fatal("expected delivery token")
	}
	delivery, err := svc.DeliveryByToken(ctx, res.Grant.Token)
	if err != nil {
		t.Fatalf("DeliveryByToken: %v", err)
	}
	if delivery.Grant.DeliveryRef != "asset-points" {
		t.Fatalf("delivery ref = %q, want asset-points", delivery.Grant.DeliveryRef)
	}
	if delivery.Item.TitleSnapshot != "Points Template" {
		t.Fatalf("title snapshot = %q, want Points Template", delivery.Item.TitleSnapshot)
	}
	if delivery.DownloadURL == "" {
		t.Fatal("expected signed download URL")
	}
	u, err := url.Parse(delivery.DownloadURL)
	if err != nil {
		t.Fatalf("parse download url: %v", err)
	}
	if u.Scheme != "https" || u.Host != "shop.example" {
		t.Fatalf("download url host = %s://%s", u.Scheme, u.Host)
	}
	if !strings.Contains(u.Path, "/api/v1/delivery/") || !strings.HasSuffix(u.Path, "/download") {
		t.Fatalf("download url path = %q", u.Path)
	}
	exp := u.Query().Get("exp")
	sig := u.Query().Get("sig")
	download, err := svc.ResolveDeliveryDownload(ctx, res.Grant.Token, exp, sig)
	if err != nil {
		t.Fatalf("ResolveDeliveryDownload: %v", err)
	}
	if download.DeliveryRef != "asset-points" {
		t.Fatalf("download delivery ref = %q, want asset-points", download.DeliveryRef)
	}
	if download.URL != "https://asset.example/grants/download-token" {
		t.Fatalf("download url = %q, want asset grant url", download.URL)
	}
	if assetDelivery.assetID != "asset-points" || assetDelivery.subjectID != sub {
		t.Fatalf("asset delivery input = asset %q subject %q, want asset-points/%s", assetDelivery.assetID, assetDelivery.subjectID, sub)
	}
	if _, err := svc.ResolveDeliveryDownload(ctx, res.Grant.Token, exp, sig+"x"); err == nil {
		t.Fatal("expected tampered signature to fail")
	}
	p, err := pg.GetProductByExternal(ctx, "shop", externalID)
	if err != nil {
		t.Fatalf("GetProductByExternal: %v", err)
	}
	ok, err := pg.EntitlementExists(ctx, sub, p.ID)
	if err != nil {
		t.Fatalf("EntitlementExists: %v", err)
	}
	if !ok {
		t.Fatal("expected entitlement for points checkout")
	}
}

func TestPurchasesListsLoggedInDeliveryGrants(t *testing.T) {
	svc, _, ctx := newSvc(t)
	sub := uid("purchases-sub")
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub:   sub,
		BuyerEmail: "library@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   uid("variant-library"),
			VariantID:    uid("variant-id-library"),
			Title:        "Library Pack",
			VariantTitle: "Standard",
			SKU:          "LIB-STD",
			PriceCents:   1200,
			Currency:     "CNY",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-library",
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if _, err := svc.SettleCheckout(ctx, order.OrderNo, "dev", "dev-library", 1200); err != nil {
		t.Fatalf("SettleCheckout: %v", err)
	}
	purchases, err := svc.Purchases(ctx, sub, 10, 0)
	if err != nil {
		t.Fatalf("Purchases: %v", err)
	}
	if len(purchases) != 1 {
		t.Fatalf("purchases len = %d, want 1", len(purchases))
	}
	if purchases[0].Item.SKUSnapshot != "LIB-STD" {
		t.Fatalf("purchase sku = %q, want LIB-STD", purchases[0].Item.SKUSnapshot)
	}
}

func TestAdminOrderSupportActions(t *testing.T) {
	svc, _, ctx := newSvc(t)
	mailer := &captureDeliveryMailer{}
	svc.ConfigureDeliveryMailer(mailer)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerEmail: "support@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   uid("variant-support"),
			VariantID:    uid("variant-id-support"),
			Title:        "Support Pack",
			VariantTitle: "Standard",
			SKU:          "SUP-STD",
			PriceCents:   2500,
			Currency:     "CNY",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-support",
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if _, err := svc.SettleCheckout(ctx, order.OrderNo, "alipay", "pay-tx-support", 2500); err != nil {
		t.Fatalf("SettleCheckout: %v", err)
	}
	if len(mailer.mails) != 1 {
		t.Fatalf("delivery mails after settle = %d, want 1", len(mailer.mails))
	}

	resent, err := svc.ResendDelivery(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("ResendDelivery: %v", err)
	}
	if resent.Token == "" || resent.DeliveryRef != "asset-support" {
		t.Fatalf("unexpected resend grant: %+v", resent)
	}
	if len(mailer.mails) != 2 {
		t.Fatalf("delivery mails after resend = %d, want 2", len(mailer.mails))
	}
	detail, err := svc.OrderDetail(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("OrderDetail after resend: %v", err)
	}
	if got := countGrantsByState(detail.Grants, "active"); got != 2 {
		t.Fatalf("active grants after resend = %d, want 2", got)
	}
	if got := countEventsByType(detail.Events, "delivery_grant"); got != 1 {
		t.Fatalf("delivery_grant events after resend = %d, want 1", got)
	}

	revoked, err := svc.RevokeDelivery(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("RevokeDelivery: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("revoked grants = %d, want 2", revoked)
	}
	detail, err = svc.OrderDetail(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("OrderDetail after revoke: %v", err)
	}
	if got := countGrantsByState(detail.Grants, "revoked"); got != 2 {
		t.Fatalf("revoked grants after revoke = %d, want 2", got)
	}
	if got := countEventsByType(detail.Events, "delivery_revoke"); got != 1 {
		t.Fatalf("delivery_revoke events = %d, want 1", got)
	}

	manual, err := svc.GrantDelivery(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("GrantDelivery: %v", err)
	}
	if manual.Token == "" || manual.DeliveryRef != "asset-support" {
		t.Fatalf("unexpected manual grant: %+v", manual)
	}
	if len(mailer.mails) != 2 {
		t.Fatalf("delivery mails after manual grant = %d, want 2", len(mailer.mails))
	}
	if err := svc.MarkRefunded(ctx, order.OrderNo, "refund-support"); err != nil {
		t.Fatalf("MarkRefunded: %v", err)
	}
	detail, err = svc.OrderDetail(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("OrderDetail after refund: %v", err)
	}
	if detail.Order.Status != model.OrderStatusRefunded {
		t.Fatalf("order status = %q, want refunded", detail.Order.Status)
	}
	if detail.Order.DeliveryState != model.DeliveryStateRevoked {
		t.Fatalf("delivery state = %q, want revoked", detail.Order.DeliveryState)
	}
	if got := countGrantsByState(detail.Grants, "active"); got != 0 {
		t.Fatalf("active grants after refund = %d, want 0", got)
	}
	if got := countEventsByType(detail.Events, "refund"); got != 1 {
		t.Fatalf("refund events = %d, want 1", got)
	}
}

func countGrantsByState(grants []*model.DeliveryGrant, state string) int {
	count := 0
	for _, grant := range grants {
		if grant.State == state {
			count++
		}
	}
	return count
}

func countEventsByType(events []*model.PaymentEvent, eventType string) int {
	count := 0
	for _, event := range events {
		if event.EventType == eventType {
			count++
		}
	}
	return count
}

type captureDeliveryMailer struct {
	mails []service.DeliveryMail
}

func (m *captureDeliveryMailer) SendDelivery(ctx context.Context, in service.DeliveryMail) error {
	m.mails = append(m.mails, in)
	return nil
}

type captureAssetDelivery struct {
	url       string
	assetID   string
	subjectID string
}

func (c *captureAssetDelivery) CreateDelivery(ctx context.Context, in service.AssetDeliveryInput) (service.AssetDeliveryOutput, error) {
	c.assetID = in.AssetID
	c.subjectID = in.SubjectID
	return service.AssetDeliveryOutput{URL: c.url, ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}
