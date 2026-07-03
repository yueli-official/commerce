package service_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"

	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/service"
)

var errAssetSigningUnavailable = errors.New("asset signing unavailable")

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

func TestGuestCheckoutReusesRecentPayingOrder(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	item := service.CheckoutItemDesc{
		SiteKey:      "shop",
		ExternalID:   uid("variant-reuse"),
		VariantID:    uid("variant-id-reuse"),
		Title:        "Reusable Pack",
		VariantTitle: "Standard",
		SKU:          "REUSE-STD",
		PriceCents:   1900,
		Currency:     "CNY",
		DeliveryKind: "asset_file",
		DeliveryRef:  "asset-reuse",
		Quantity:     1,
	}
	first, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerEmail: "reuse@example.com",
		Provider:   "alipay",
		Items:      []service.CheckoutItemDesc{item},
	})
	if err != nil {
		t.Fatalf("CreateCheckout first: %v", err)
	}
	second, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerEmail: " reuse@example.com ",
		Provider:   "alipay",
		Items:      []service.CheckoutItemDesc{item},
	})
	if err != nil {
		t.Fatalf("CreateCheckout second: %v", err)
	}
	if second.OrderNo != first.OrderNo {
		t.Fatalf("second orderNo = %q, want reused %q", second.OrderNo, first.OrderNo)
	}
	orders, total, err := pg.ListOrders(ctx, model.OrderStatusPaying, "reuse@example.com", 10, 0)
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("paying orders = %d, want 1", len(orders))
	}
	if total != 1 {
		t.Fatalf("paying order total = %d, want 1", total)
	}
}

func TestCheckoutUsesAuthoritativeCatalogSnapshotWhenResolverConfigured(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	externalID := uid("product-authoritative")
	variantID := uid("variant-authoritative")
	svc.ConfigureCurrentCheckoutItemResolver(staticCheckoutItemResolver{
		out: service.CurrentCheckoutItemResult{
			SiteKey:               "shop",
			ExternalID:            externalID,
			VariantID:             variantID,
			Title:                 "Authoritative Pack",
			VariantTitle:          "Standard",
			SKU:                   "AUTH-STD",
			PriceCents:            4900,
			PointsCost:            30,
			Currency:              "CNY",
			DeliveryKind:          "bundle",
			DeliveryRef:           `{"access":{"maxDownloads":1},"items":[{"kind":"asset_file","assetId":"asset-real","enabled":true}]}`,
			PurchaseLimitPerBuyer: 1,
		},
	})

	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerEmail: "tamper@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:               "shop",
			ExternalID:            externalID,
			VariantID:             variantID,
			Title:                 "Tampered Pack",
			VariantTitle:          "Hacked",
			SKU:                   "HACKED",
			PriceCents:            1,
			Currency:              "CNY",
			DeliveryKind:          "asset_file",
			DeliveryRef:           "asset-tampered",
			PurchaseLimitPerBuyer: 0,
			Quantity:              1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if order.AmountCents != 4900 {
		t.Fatalf("amount cents = %d, want authoritative 4900", order.AmountCents)
	}
	items, err := pg.OrderItems(ctx, order.ID)
	if err != nil {
		t.Fatalf("OrderItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	item := items[0]
	if item.TitleSnapshot != "Authoritative Pack" || item.SKUSnapshot != "AUTH-STD" {
		t.Fatalf("item snapshot = %+v, want authoritative title/sku", item)
	}
	if item.UnitPriceCents != 4900 || item.DeliveryKindSnapshot != "bundle" || !strings.Contains(item.DeliveryRefSnapshot, "asset-real") {
		t.Fatalf("price/delivery snapshot = %+v, want authoritative snapshot", item)
	}
	if !strings.Contains(item.DeliveryRefSnapshot, `"maxDownloads":1`) {
		t.Fatalf("delivery access missing in snapshot: %s", item.DeliveryRefSnapshot)
	}
}

func TestGuestCheckoutCancelRequiresBuyerAndUpdatesStatus(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerEmail: "cancel@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   uid("variant-cancel"),
			VariantID:    uid("variant-id-cancel"),
			Title:        "Cancel Pack",
			VariantTitle: "Standard",
			SKU:          "CANCEL-STD",
			PriceCents:   990,
			Currency:     "CNY",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-cancel",
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if _, err := svc.CancelCheckout(ctx, order.OrderNo, "", "other@example.com"); !isCoded(err, commerceerr.CodeForbidden) {
		t.Fatalf("CancelCheckout wrong buyer: want forbidden, got %v", err)
	}
	cancelled, err := svc.CancelCheckout(ctx, order.OrderNo, "", " cancel@example.com ")
	if err != nil {
		t.Fatalf("CancelCheckout: %v", err)
	}
	if cancelled.Status != model.OrderStatusCancelled {
		t.Fatalf("cancelled status = %q, want cancelled", cancelled.Status)
	}
	loaded, err := pg.GetOrderByNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("GetOrderByNo: %v", err)
	}
	if loaded.Status != model.OrderStatusCancelled {
		t.Fatalf("db status = %q, want cancelled", loaded.Status)
	}
}

func TestPaymentMethodConfigControlsCheckoutCreation(t *testing.T) {
	svc, _, ctx := newSvc(t)
	enabled, err := svc.PaymentMethodEnabled(ctx, "alipay")
	if err != nil {
		t.Fatalf("PaymentMethodEnabled default: %v", err)
	}
	if !enabled {
		t.Fatal("alipay should be enabled by default migration seed")
	}
	methods, err := svc.SavePaymentMethods(ctx, []service.PaymentMethodInput{{
		Provider:  "alipay",
		Label:     "Alipay",
		Enabled:   false,
		SortOrder: 10,
	}})
	if err != nil {
		t.Fatalf("SavePaymentMethods: %v", err)
	}
	foundAlipay := false
	for _, method := range methods {
		if method.Provider == "alipay" {
			foundAlipay = true
			if method.Enabled {
				t.Fatal("alipay should be disabled after admin save")
			}
		}
	}
	if !foundAlipay {
		t.Fatal("alipay method missing after save")
	}
	_, err = svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerEmail: "disabled@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   uid("variant-disabled"),
			VariantID:    uid("variant-id-disabled"),
			Title:        "Disabled Pack",
			VariantTitle: "Standard",
			SKU:          "DISABLED-STD",
			PriceCents:   990,
			Currency:     "CNY",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-disabled",
			Quantity:     1,
		}},
	})
	if !isCoded(err, commerceerr.CodeInvalidRequest) {
		t.Fatalf("CreateCheckout with disabled method: want invalid_request, got %v", err)
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
	purchases, total, err := svc.Purchases(ctx, service.PurchaseFilter{Sub: sub}, 10, 0)
	if err != nil {
		t.Fatalf("Purchases: %v", err)
	}
	if len(purchases) != 1 {
		t.Fatalf("purchases len = %d, want 1", len(purchases))
	}
	if total != 1 {
		t.Fatalf("purchases total = %d, want 1", total)
	}
	if purchases[0].Item.SKUSnapshot != "LIB-STD" {
		t.Fatalf("purchase sku = %q, want LIB-STD", purchases[0].Item.SKUSnapshot)
	}
}

func TestResolvePurchaseDownloadSelectsAssetFromBundle(t *testing.T) {
	svc, _, ctx := newSvc(t)
	assetDelivery := &captureAssetDelivery{url: "https://asset.example/grants/bundle-token"}
	svc.ConfigureAssetDeliveryClient(assetDelivery)
	sub := uid("bundle-buyer")
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub:   sub,
		BuyerEmail: "bundle@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   uid("product-bundle"),
			VariantID:    uid("variant-bundle"),
			Title:        "Bundle Pack",
			VariantTitle: "Standard",
			SKU:          "BUNDLE-STD",
			PriceCents:   1200,
			Currency:     "CNY",
			DeliveryKind: "bundle",
			DeliveryRef:  `{"items":[{"kind":"asset_file","assetId":"asset-a","enabled":true},{"kind":"asset_file","assetId":"asset-b","enabled":true}]}`,
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if _, err := svc.SettleCheckout(ctx, order.OrderNo, "dev", "dev-bundle", 1200); err != nil {
		t.Fatalf("SettleCheckout: %v", err)
	}
	download, err := svc.ResolvePurchaseDownload(ctx, sub, order.OrderNo, "asset-b")
	if err != nil {
		t.Fatalf("ResolvePurchaseDownload: %v", err)
	}
	if download.DeliveryRef != "asset-b" {
		t.Fatalf("delivery ref = %q, want asset-b", download.DeliveryRef)
	}
	if assetDelivery.assetID != "asset-b" {
		t.Fatalf("asset delivery asset = %q, want asset-b", assetDelivery.assetID)
	}
	if _, err := svc.ResolvePurchaseDownload(ctx, sub, order.OrderNo, "asset-missing"); err == nil {
		t.Fatal("expected missing bundle asset to fail")
	}
}

func TestResolvePurchaseDownloadDoesNotConsumeDownloadLimitWhenAssetSigningFails(t *testing.T) {
	svc, _, ctx := newSvc(t)
	assetDelivery := &flakyAssetDelivery{url: "https://asset.example/grants/retry-token"}
	svc.ConfigureAssetDeliveryClient(assetDelivery)
	sub := uid("download-retry-buyer")
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerSub:   sub,
		BuyerEmail: "download-retry@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   uid("product-download-retry"),
			VariantID:    uid("variant-download-retry"),
			Title:        "Retry Pack",
			VariantTitle: "Standard",
			SKU:          "RETRY-STD",
			PriceCents:   1200,
			Currency:     "CNY",
			DeliveryKind: "bundle",
			DeliveryRef:  `{"access":{"maxDownloads":1},"items":[{"kind":"asset_file","assetId":"asset-retry","enabled":true}]}`,
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if _, err := svc.SettleCheckout(ctx, order.OrderNo, "dev", "dev-download-retry", 1200); err != nil {
		t.Fatalf("SettleCheckout: %v", err)
	}
	if _, err := svc.ResolvePurchaseDownload(ctx, sub, order.OrderNo, "asset-retry"); err == nil {
		t.Fatal("expected first signing attempt to fail")
	}
	download, err := svc.ResolvePurchaseDownload(ctx, sub, order.OrderNo, "asset-retry")
	if err != nil {
		t.Fatalf("second ResolvePurchaseDownload should still be allowed: %v", err)
	}
	if download.URL != "https://asset.example/grants/retry-token" {
		t.Fatalf("download url = %q, want retry token", download.URL)
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

func TestRecordPaymentFailureAppearsInOrderDetail(t *testing.T) {
	svc, _, ctx := newSvc(t)
	order, err := svc.CreateCheckout(ctx, service.CheckoutDesc{
		BuyerEmail: "audit@example.com",
		Provider:   "paypal",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   uid("variant-audit"),
			VariantID:    uid("variant-id-audit"),
			Title:        "Audit Pack",
			VariantTitle: "Standard",
			SKU:          "AUDIT-STD",
			PriceCents:   4200,
			Currency:     "USD",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-audit",
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}

	if err := svc.RecordPaymentFailure(ctx, order.OrderNo, "paypal", "capture", "PAYPAL-ORDER-1", 4200, "paypal capture amount mismatch"); err != nil {
		t.Fatalf("RecordPaymentFailure: %v", err)
	}
	detail, err := svc.OrderDetail(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("OrderDetail: %v", err)
	}
	if len(detail.Events) != 1 {
		t.Fatalf("events len = %d, want 1", len(detail.Events))
	}
	event := detail.Events[0]
	if event.Provider != "paypal" || event.EventType != "capture" || event.Success {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.ProviderEventID != "PAYPAL-ORDER-1" || event.AmountCents != 4200 {
		t.Fatalf("unexpected event payload: %+v", event)
	}
	if event.Message != "paypal capture amount mismatch" {
		t.Fatalf("event message = %q", event.Message)
	}
}

func TestFreeCheckoutCreatesFulfilledOrderAndDelivery(t *testing.T) {
	svc, pg, ctx := newSvc(t)
	mailer := &captureDeliveryMailer{}
	svc.ConfigureDelivery(service.DeliveryConfig{
		SigningSecret: "test-secret",
		PublicBaseURL: "https://shop.example",
		TTL:           time.Minute,
	})
	svc.ConfigureDeliveryMailer(mailer)
	sub := uid("free-buyer")
	externalID := uid("variant-free")
	res, err := svc.ClaimFreeCheckout(ctx, service.CheckoutDesc{
		BuyerSub:   sub,
		BuyerEmail: "free@example.com",
		Items: []service.CheckoutItemDesc{{
			SiteKey:      "shop",
			ExternalID:   externalID,
			VariantID:    uid("variant-id-free"),
			Title:        "Community Pack",
			VariantTitle: "Community",
			SKU:          "COMM-FREE",
			PriceCents:   0,
			PointsCost:   0,
			Currency:     "CNY",
			DeliveryKind: "asset_file",
			DeliveryRef:  "asset-free",
			Quantity:     1,
		}},
	})
	if err != nil {
		t.Fatalf("ClaimFreeCheckout: %v", err)
	}
	if res.Order.Status != model.OrderStatusFulfilled {
		t.Fatalf("returned order status = %q, want fulfilled", res.Order.Status)
	}
	if res.Grant.Token == "" {
		t.Fatal("expected delivery token")
	}
	if res.Grant.DeliveryRef != "asset-free" {
		t.Fatalf("delivery ref = %q, want asset-free", res.Grant.DeliveryRef)
	}
	loaded, err := pg.GetOrderByNo(ctx, res.Order.OrderNo)
	if err != nil {
		t.Fatalf("GetOrderByNo: %v", err)
	}
	if loaded.Status != model.OrderStatusFulfilled {
		t.Fatalf("db order status = %q, want fulfilled", loaded.Status)
	}
	if loaded.PaymentProvider != model.ProductKindFree || loaded.AmountCents != 0 {
		t.Fatalf("unexpected free order payment snapshot: provider=%q amount=%d", loaded.PaymentProvider, loaded.AmountCents)
	}
	p, err := pg.GetProductByExternal(ctx, "shop", externalID)
	if err != nil {
		t.Fatalf("GetProductByExternal: %v", err)
	}
	if p == nil || p.Kind != model.ProductKindFree {
		t.Fatalf("product kind = %+v, want free product", p)
	}
	ok, err := pg.EntitlementExists(ctx, sub, p.ID)
	if err != nil {
		t.Fatalf("EntitlementExists: %v", err)
	}
	if !ok {
		t.Fatal("expected entitlement for logged-in free claim")
	}
	if len(mailer.mails) != 1 {
		t.Fatalf("delivery mails = %d, want 1", len(mailer.mails))
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

type flakyAssetDelivery struct {
	url      string
	attempts int
}

func (c *flakyAssetDelivery) CreateDelivery(ctx context.Context, in service.AssetDeliveryInput) (service.AssetDeliveryOutput, error) {
	c.attempts++
	if c.attempts == 1 {
		return service.AssetDeliveryOutput{}, errAssetSigningUnavailable
	}
	return service.AssetDeliveryOutput{URL: c.url, ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}

type staticCheckoutItemResolver struct {
	out service.CurrentCheckoutItemResult
	err error
}

func (r staticCheckoutItemResolver) CurrentCheckoutItem(ctx context.Context, in service.CurrentCheckoutItemInput) (service.CurrentCheckoutItemResult, error) {
	return r.out, r.err
}
