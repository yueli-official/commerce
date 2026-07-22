package server_test

// HTTP integration test for the commerce service.
// Requires a live PostgreSQL instance: set COMMERCE_PG_HOST (and optionally
// COMMERCE_PG_PORT / COMMERCE_PG_USER / COMMERCE_PG_PASS / COMMERCE_PG_DB) to run.
// The schema is rebuilt in COMMERCE_PG_DB, defaulting to commerce_test.
//
// Run:
//
//	COMMERCE_PG_HOST=192.168.5.5 COMMERCE_PG_USER=postgres COMMERCE_PG_PASS=postgres \
//	  go test -p 1 ./internal/server/...

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/encoding/gjson"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/test/gtest"
	_ "github.com/lib/pq"

	foundationauth "github.com/yueli-official/foundation/go/auth"
	"platform/gokit/authsetup"
	"platform/paykit"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/server"
	"platform/services/commerce/internal/service"
)

// ─── test constants ──────────────────────────────────────────────────────────

const (
	testIssuer   = "http://localhost:8081"
	testKID      = "commerce-test-kid"
	testSubAlice = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	testSubBob   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	fakePayURL   = "https://fakepay.example.com/pay?token=TEST"
	fakeSiteKey  = "resource"
	fakeExtID    = "res-test-001"
)

// ─── fake gateway ────────────────────────────────────────────────────────────

// fakeGateway is an in-memory PaymentGateway for tests.
type fakeGateway struct {
	// successBodies maps raw body bytes to the NotifyOut to return.
	// When nil, any body with the sentinel prefix triggers success.
	successBody []byte
	queryOut    paykit.QueryPaymentOut
	queryCalls  []paykit.QueryPaymentIn
}

type testCheckoutResolver struct{}

func (testCheckoutResolver) CurrentCheckoutItem(_ context.Context, in service.CurrentCheckoutItemInput) (service.CurrentCheckoutItemResult, error) {
	out := service.CurrentCheckoutItemResult{
		SiteKey:      in.SiteKey,
		ExternalID:   in.ExternalID,
		VariantID:    in.VariantID,
		Title:        "Test Article",
		VariantTitle: "Basic",
		SKU:          "TEST-BASIC",
		PriceCents:   9900,
		Currency:     "CNY",
		DeliveryKind: "asset_file",
		DeliveryRef:  "asset-" + in.ExternalID,
	}
	switch in.ExternalID {
	case "sync-checkout-001":
		out.Title = "Sync Checkout"
		out.PriceCents = 100
		out.DeliveryKind = "netdisk"
		out.DeliveryRef = `{"netdisk":{"provider":"manual","url":"https://example.test/download"}}`
	case "pts-1":
		out.Title = "Points Resource"
		out.PriceCents = 0
		out.PointsCost = 5
		out.Currency = "POINTS"
		out.PurchaseLimitPerBuyer = 1
	case "pts-2":
		out.Title = "Too Pricey"
		out.PriceCents = 0
		out.PointsCost = 9999
		out.Currency = "POINTS"
		out.PurchaseLimitPerBuyer = 1
	}
	return out, nil
}

// failingGateway always returns an error from CreatePayment, simulating a
// gateway outage.  Used to test the order-cancel-on-failure path.
type failingGateway struct{}

func (f *failingGateway) Name() string {
	return "alipay"
}

func (f *failingGateway) CreatePayment(_ context.Context, _ paykit.CreatePaymentIn) (*paykit.CreatePaymentOut, error) {
	return nil, fmt.Errorf("simulated gateway failure")
}

func (f *failingGateway) CapturePayment(context.Context, paykit.CapturePaymentIn) (*paykit.CapturePaymentOut, error) {
	return nil, fmt.Errorf("simulated gateway failure")
}

func (f *failingGateway) VerifyNotify(_ context.Context, _ []byte, _ map[string]string) (*paykit.NotifyOut, error) {
	return nil, fmt.Errorf("simulated gateway failure")
}

func (f *failingGateway) Refund(context.Context, paykit.RefundIn) (*paykit.RefundOut, error) {
	return nil, fmt.Errorf("simulated gateway failure")
}

func (f *fakeGateway) CreatePayment(_ context.Context, in paykit.CreatePaymentIn) (*paykit.CreatePaymentOut, error) {
	// Record the order number in the sentinel body so VerifyNotify can match.
	f.successBody = []byte("order=" + in.OrderNo)
	return &paykit.CreatePaymentOut{Provider: "alipay", Method: string(paykit.CapabilityRedirect), PayURL: fakePayURL}, nil
}

func (f *fakeGateway) Name() string {
	return "alipay"
}

func (f *fakeGateway) CapturePayment(context.Context, paykit.CapturePaymentIn) (*paykit.CapturePaymentOut, error) {
	return nil, paykit.ErrUnsupportedOperation
}

func (f *fakeGateway) VerifyNotify(_ context.Context, body []byte, _ map[string]string) (*paykit.NotifyOut, error) {
	if f.successBody != nil && bytes.Equal(body, f.successBody) {
		// Extract order number from "order=<orderNo>"
		orderNo := string(body[len("order="):])
		return &paykit.NotifyOut{
			Success:      true,
			OrderNo:      orderNo,
			ProviderTxID: "FAKE-TX-" + orderNo,
			AmountCents:  9900, // must match what CreateOrder stores
		}, nil
	}
	return &paykit.NotifyOut{Success: false}, nil
}

func (f *fakeGateway) QueryPayment(_ context.Context, in paykit.QueryPaymentIn) (*paykit.QueryPaymentOut, error) {
	f.queryCalls = append(f.queryCalls, in)
	out := f.queryOut
	if out.OrderNo == "" {
		out.OrderNo = in.OrderNo
	}
	return &out, nil
}

func (f *fakeGateway) Refund(context.Context, paykit.RefundIn) (*paykit.RefundOut, error) {
	return nil, paykit.ErrUnsupportedOperation
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func mustVerifier(t *gtest.T, priv *rsa.PrivateKey) *foundationauth.Verifier {
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: priv.Public(), KeyID: testKID, Algorithm: "RS256", Use: "sig",
	}}}
	v, err := authsetup.NewStaticVerifier(authsetup.StaticVerifierConfig{
		Keys:   set,
		Issuer: testIssuer,
	})
	t.AssertNil(err)
	return v
}

func signToken(t *gtest.T, priv *rsa.PrivateKey, sub string) string {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", testKID),
	)
	t.AssertNil(err)
	now := time.Now().UTC()
	raw, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   testIssuer,
		Subject:  sub,
		IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)),
		Expiry:   jwt.NewNumericDate(now.Add(10 * time.Minute)),
	}).Serialize()
	t.AssertNil(err)
	return raw
}

var serverSeq int

func newTestServer(t *gtest.T, db *dao.PG, fake *fakeGateway, v *foundationauth.Verifier, devSettle bool) *ghttp.Server {
	return newTestServerWithGW(t, db, fake, v, devSettle)
}

func newTestServerWithGW(t *gtest.T, db *dao.PG, gw paykit.Provider, v *foundationauth.Verifier, devSettle bool) *ghttp.Server {
	serverSeq++
	name := fmt.Sprintf("%s-%d", t.Name(), serverSeq)
	reg := paykit.Registry{"alipay": gw}
	s := g.Server(name)
	s.SetAddr("127.0.0.1:0")
	server.Configure(s, server.Deps{
		Verifier:        v,
		DB:              db,
		Registry:        reg,
		NotifyURL:       "http://localhost:8084/api/v1/payments/alipay/notify",
		ReturnURL:       "http://localhost:3000/pay/return",
		DevSettle:       devSettle,
		Checkin:         service.CheckinConfig{Base: 10, Step: 2, Cap: 30},
		CurrentCheckout: testCheckoutResolver{},
	})
	s.SetDumpRouterMap(false)
	s.Start()
	return s
}

func prefix(s *ghttp.Server) string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

func TestLegacyOrdersRouteRemoved(t *testing.T) {
	gtest.C(t, func(t *gtest.T) {
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		t.AssertNil(err)
		v := mustVerifier(t, priv)

		serverSeq++
		s := g.Server(fmt.Sprintf("%s-legacy-orders-%d", t.Name(), serverSeq))
		s.SetAddr("127.0.0.1:0")
		server.Configure(s, server.Deps{Verifier: v})
		s.SetDumpRouterMap(false)
		s.Start()
		defer s.Shutdown()

		c := g.Client()
		c.SetPrefix(prefix(s))
		resp, err := c.Post(context.Background(), "/api/v1/orders", `{"siteKey":"shop"}`)
		t.AssertNil(err)
		defer resp.Close()
		t.Assert(resp.StatusCode, 404)
	})
}

// pgSetup creates the commerce DB + schema fresh.
func pgSetup(t *gtest.T) *dao.PG {
	t.Helper()
	host := envOr("COMMERCE_PG_HOST", "")
	if host == "" {
		t.Skip("set COMMERCE_PG_HOST to run commerce HTTP integration tests")
	}
	port := envOr("COMMERCE_PG_PORT", "5432")
	user := envOr("COMMERCE_PG_USER", "postgres")
	pass := envOr("COMMERCE_PG_PASS", "")
	dbName := envOr("COMMERCE_PG_DB", "commerce_test")

	// Ensure the isolated commerce test database exists.
	mdb, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=postgres sslmode=disable",
		host, port, user, pass))
	t.AssertNil(err)
	_, _ = mdb.Exec("CREATE DATABASE " + dbName)
	mdb.Close()

	// Connect to the isolated test DB and (re)apply schema.
	cdb, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, pass, dbName))
	t.AssertNil(err)
	_, _ = cdb.Exec("DROP TABLE IF EXISTS checkin_records CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS credits_ledger CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS credits_balances CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS commerce_payment_methods CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS payment_events CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS delivery_grants CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS order_items CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS commerce_buyers CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS entitlements CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS orders CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS products CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS schema_migrations CASCADE")
	migrations, err := filepath.Glob("../../manifest/sql/migrations/*.up.sql")
	t.AssertNil(err)
	sort.Strings(migrations)
	for _, migrationPath := range migrations {
		migration, readErr := os.ReadFile(migrationPath)
		t.AssertNil(readErr)
		_, execErr := cdb.Exec(string(migration))
		t.AssertNil(execErr)
	}
	cdb.Close()

	db, err := gdb.New(gdb.ConfigNode{
		Type: "pgsql",
		Host: host,
		Port: port,
		User: user,
		Pass: pass,
		Name: dbName,
	})
	t.AssertNil(err)
	return dao.NewPG(db)
}

// ─── tests ───────────────────────────────────────────────────────────────────

// TestCommerceHTTP runs all HTTP integration scenarios as sub-tests.
func TestCommerceHTTP(t *testing.T) {
	gtest.C(t, func(t *gtest.T) {
		db := pgSetup(t)
		ctx := context.Background()

		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		t.AssertNil(err)
		v := mustVerifier(t, priv)
		aliceToken := signToken(t, priv, testSubAlice)

		fake := &fakeGateway{}

		// Server with devSettle enabled.
		s := newTestServer(t, db, fake, v, true)
		defer s.Shutdown()
		base := prefix(s)

		// ── 1. Legacy order route is removed ──────────────────────────────────
		c := g.Client()
		c.SetPrefix(base)
		rawResp, err := c.Post(ctx, "/api/v1/orders", `{
			"siteKey":"resource","externalId":"res-no-auth","kind":"paid",
			"priceCents":9900,"title":"Test Article","currency":"CNY"
		}`)
		t.AssertNil(err)
		defer rawResp.Close()
		t.Assert(rawResp.StatusCode, 404)

		// ── 2. CreateCheckout with valid JWT → 200 + orderNo + payUrl ─────────
		authC := g.Client()
		authC.SetPrefix(base)
		authC.SetHeader("Authorization", "Bearer "+aliceToken)
		authC.SetHeader("Content-Type", "application/json")

		createBody := fmt.Sprintf(`{
			"provider":"alipay",
			"items":[{
				"siteKey":%q,"externalId":%q,"variantId":"basic",
				"priceCents":1,"title":"Tampered Title","currency":"USD","quantity":1
			}]
		}`, fakeSiteKey, fakeExtID)

		createResp, err := authC.Post(ctx, "/api/v1/checkouts", createBody)
		t.AssertNil(err)
		defer createResp.Close()
		t.Assert(createResp.StatusCode, 200)
		jCreate := gjson.New(createResp.ReadAllString())
		orderNo := jCreate.Get("orderNo").String()
		payURL := jCreate.Get("payUrl").String()
		t.AssertNE(orderNo, "")
		t.Assert(payURL, fakePayURL)

		// ── 3. GET /access for unpurchased → {entitled:false, reason:not_purchased}
		accResp, err := authC.Get(ctx, fmt.Sprintf("/api/v1/access?siteKey=%s&externalId=%s", fakeSiteKey, fakeExtID))
		t.AssertNil(err)
		defer accResp.Close()
		t.Assert(accResp.StatusCode, 200)
		jAcc := gjson.New(accResp.ReadAllString())
		t.Assert(jAcc.Get("entitled").Bool(), false)
		t.Assert(jAcc.Get("reason").String(), "not_purchased")

		// ── 4. Dev settle → order becomes paid ────────────────────────────────
		settleC := g.Client()
		settleC.SetPrefix(base)
		settleResp, err := settleC.Post(ctx, "/dev/orders/"+orderNo+"/settle", nil)
		t.AssertNil(err)
		defer settleResp.Close()
		t.Assert(settleResp.StatusCode, 200)
		_ = settleResp.ReadAllString()

		// ── 5. GET /access now → {entitled:true, reason:ok} ──────────────────
		accResp2, err := authC.Get(ctx, fmt.Sprintf("/api/v1/access?siteKey=%s&externalId=%s", fakeSiteKey, fakeExtID))
		t.AssertNil(err)
		defer accResp2.Close()
		t.Assert(accResp2.StatusCode, 200)
		jAcc2 := gjson.New(accResp2.ReadAllString())
		t.Assert(jAcc2.Get("entitled").Bool(), true)
		t.Assert(jAcc2.Get("reason").String(), "ok")

		// ── 6. Notify idempotency ─────────────────────────────────────────────
		// Create a second order for a fresh product to test notify idempotency.
		notifyExtID := "res-notify-idem-001"
		createBody2 := fmt.Sprintf(`{
			"provider":"alipay",
			"items":[{
				"siteKey":%q,"externalId":%q,"variantId":"basic",
				"priceCents":1,"title":"Tampered Notify","currency":"USD","quantity":1
			}]
		}`, fakeSiteKey, notifyExtID)
		createResp2, err := authC.Post(ctx, "/api/v1/checkouts", createBody2)
		t.AssertNil(err)
		defer createResp2.Close()
		t.Assert(createResp2.StatusCode, 200)
		jCreate2 := gjson.New(createResp2.ReadAllString())
		orderNo2 := jCreate2.Get("orderNo").String()
		t.AssertNE(orderNo2, "")

		// fake.successBody was set by the second CreatePayment call.
		notifyBody := fake.successBody

		// First notify call.
		notifyC := g.Client()
		notifyC.SetPrefix(base)
		nr1, err := notifyC.Post(ctx, "/api/v1/payments/alipay/notify", notifyBody)
		t.AssertNil(err)
		defer nr1.Close()
		t.Assert(nr1.StatusCode, 200)
		t.Assert(nr1.ReadAllString(), "success")

		// Second notify call (replay).
		nr2, err := notifyC.Post(ctx, "/api/v1/payments/alipay/notify", notifyBody)
		t.AssertNil(err)
		defer nr2.Close()
		t.Assert(nr2.StatusCode, 200)
		t.Assert(nr2.ReadAllString(), "success")

		// Access check after notify confirms entitlement (both replays → entitled once).
		accResp3, err := authC.Get(ctx, fmt.Sprintf("/api/v1/access?siteKey=%s&externalId=%s", fakeSiteKey, notifyExtID))
		t.AssertNil(err)
		defer accResp3.Close()
		t.Assert(accResp3.StatusCode, 200)
		jAcc3 := gjson.New(accResp3.ReadAllString())
		t.Assert(jAcc3.Get("entitled").Bool(), true)
		t.Assert(jAcc3.Get("reason").String(), "ok")

		// ── 7. Dev settle absent when devSettle=false ────────────────────────
		s2 := newTestServer(t, db, &fakeGateway{}, v, false)
		defer s2.Shutdown()
		base2 := fmt.Sprintf("http://127.0.0.1:%d", s2.GetListenedPort())
		c2 := g.Client()
		c2.SetPrefix(base2)
		// any orderNo → should 404 (route not registered)
		sr, err := c2.Post(ctx, "/dev/orders/NONEXISTENT/settle", nil)
		t.AssertNil(err)
		defer sr.Close()
		t.Assert(sr.StatusCode, 404)
	})
}

func TestCheckoutSyncPaymentQuerySettlesOrder(t *testing.T) {
	gtest.C(t, func(t *gtest.T) {
		db := pgSetup(t)
		ctx := context.Background()

		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		t.AssertNil(err)
		v := mustVerifier(t, priv)
		aliceToken := signToken(t, priv, testSubAlice)

		fake := &fakeGateway{}
		s := newTestServer(t, db, fake, v, false)
		defer s.Shutdown()
		base := prefix(s)

		authC := g.Client()
		authC.SetPrefix(base)
		authC.SetHeader("Authorization", "Bearer "+aliceToken)
		authC.SetHeader("Content-Type", "application/json")

		createBody := `{
			"provider":"alipay",
			"items":[{
				"siteKey":"resource","externalId":"sync-checkout-001","variantId":"basic",
				"title":"Sync Checkout","variantTitle":"Basic","sku":"SYNC-1",
				"priceCents":100,"currency":"CNY","deliveryKind":"netdisk",
				"deliveryRef":"{\"netdisk\":{\"provider\":\"manual\",\"url\":\"https://example.test/download\"}}",
				"quantity":1
			}]
		}`
		createResp, err := authC.Post(ctx, "/api/v1/checkouts", createBody)
		t.AssertNil(err)
		defer createResp.Close()
		t.Assert(createResp.StatusCode, 200)
		orderNo := gjson.New(createResp.ReadAllString()).Get("orderNo").String()
		t.AssertNE(orderNo, "")

		fake.queryOut = paykit.QueryPaymentOut{
			Success:      true,
			OrderNo:      orderNo,
			ProviderTxID: "ALI-SYNC-TX-1",
			AmountCents:  100,
		}
		syncResp, err := authC.Post(ctx, "/api/v1/checkouts/"+orderNo+"/sync", `{}`)
		t.AssertNil(err)
		defer syncResp.Close()
		t.Assert(syncResp.StatusCode, 200)
		jSync := gjson.New(syncResp.ReadAllString())
		t.Assert(jSync.Get("status").String(), model.OrderStatusFulfilled)
		t.Assert(jSync.Get("deliveryState").String(), "granted")
		t.AssertNE(jSync.Get("deliveryRef").String(), "")
		t.Assert(len(fake.queryCalls), 1)
		t.Assert(fake.queryCalls[0].OrderNo, orderNo)
		t.Assert(fake.queryCalls[0].AmountCents, 100)
	})
}

// TestCreateCheckout_GatewayFailure_OrderCancelled verifies that when the
// payment gateway's CreatePayment returns an error, the pending order is
// transitioned to "cancelled" rather than left as an orphan.
func TestCreateCheckout_GatewayFailure_OrderCancelled(t *testing.T) {
	gtest.C(t, func(t *gtest.T) {
		db := pgSetup(t)
		ctx := context.Background()

		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		t.AssertNil(err)
		v := mustVerifier(t, priv)
		aliceToken := signToken(t, priv, testSubAlice)

		// Use the failing gateway so CreatePayment always errors.
		s := newTestServerWithGW(t, db, &failingGateway{}, v, false)
		defer s.Shutdown()
		base := prefix(s)

		authC := g.Client()
		authC.SetPrefix(base)
		authC.SetHeader("Authorization", "Bearer "+aliceToken)
		authC.SetHeader("Content-Type", "application/json")

		createBody := `{"provider":"alipay","items":[{"siteKey":"resource","externalId":"gw-fail-001","variantId":"basic","priceCents":1,"title":"Tampered","currency":"USD","quantity":1}]}`
		resp, err := authC.Post(ctx, "/api/v1/checkouts", createBody)
		t.AssertNil(err)
		defer resp.Close()

		// Client must see 502 gateway_failed.
		t.Assert(resp.StatusCode, 502)
		j := gjson.New(resp.ReadAllString())
		t.Assert(j.Get("code").String(), "commerce.gateway_failed")

		// The pending order must now be cancelled (not left as orphan).
		// Open a raw sql.DB to verify the row directly.
		host := envOr("COMMERCE_PG_HOST", "")
		port := envOr("COMMERCE_PG_PORT", "5432")
		user := envOr("COMMERCE_PG_USER", "postgres")
		pass := envOr("COMMERCE_PG_PASS", "")
		dbName := envOr("COMMERCE_PG_DB", "commerce_test")
		rawDB, err2 := sql.Open("postgres", fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			host, port, user, pass, dbName))
		t.AssertNil(err2)
		defer rawDB.Close()

		var orderStatus string
		row := rawDB.QueryRowContext(ctx,
			`SELECT status FROM orders WHERE sub = $1 ORDER BY created_at DESC LIMIT 1`,
			testSubAlice)
		scanErr := row.Scan(&orderStatus)
		t.AssertNil(scanErr)
		t.Assert(orderStatus, model.OrderStatusCancelled)
	})
}

// TestCommerceM2_CheckinAndPoints exercises the M2 points economy end-to-end:
// daily check-in earns the streak reward, the balance/ledger reflect it, and a
// points redemption spends the balance to grant an entitlement (idempotent),
// while an unaffordable redemption is refused without charging.
func TestCommerceM2_CheckinAndPoints(t *testing.T) {
	gtest.C(t, func(t *gtest.T) {
		db := pgSetup(t)
		ctx := context.Background()

		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		t.AssertNil(err)
		v := mustVerifier(t, priv)
		aliceToken := signToken(t, priv, testSubAlice)

		s := newTestServer(t, db, &fakeGateway{}, v, false)
		defer s.Shutdown()
		base := prefix(s)

		authC := g.Client()
		authC.SetPrefix(base)
		authC.SetHeader("Authorization", "Bearer "+aliceToken)
		authC.SetHeader("Content-Type", "application/json")

		// ── 1. First check-in today → streak 1, base reward 10, balance 10 ──────
		ci, err := authC.Post(ctx, "/api/v1/checkin", nil)
		t.AssertNil(err)
		jci := gjson.New(ci.ReadAllString())
		ci.Close()
		t.Assert(jci.Get("alreadyCheckedIn").Bool(), false)
		t.Assert(jci.Get("streak").Int(), 1)
		t.Assert(jci.Get("pointsAwarded").Int(), 10)
		t.Assert(jci.Get("balance").Int(), 10)

		// ── 2. Status reflects today's check-in (no mutation) ──────────────────
		st, err := authC.Get(ctx, "/api/v1/checkin/status")
		t.AssertNil(err)
		jst := gjson.New(st.ReadAllString())
		st.Close()
		t.Assert(jst.Get("checkedInToday").Bool(), true)
		t.Assert(jst.Get("streak").Int(), 1)
		t.Assert(jst.Get("balance").Int(), 10)

		// ── 3. Second check-in same day → idempotent, no double-earn ───────────
		ci2, err := authC.Post(ctx, "/api/v1/checkin", nil)
		t.AssertNil(err)
		jci2 := gjson.New(ci2.ReadAllString())
		ci2.Close()
		t.Assert(jci2.Get("alreadyCheckedIn").Bool(), true)
		t.Assert(jci2.Get("balance").Int(), 10)

		// ── 4. Balance + ledger ────────────────────────────────────────────────
		bal, err := authC.Get(ctx, "/api/v1/credits/balance")
		t.AssertNil(err)
		t.Assert(gjson.New(bal.ReadAllString()).Get("balance").Int(), 10)
		bal.Close()

		led, err := authC.Get(ctx, "/api/v1/credits/ledger")
		t.AssertNil(err)
		jled := gjson.New(led.ReadAllString())
		led.Close()
		t.Assert(jled.Get("total").Int(), 1)
		t.Assert(jled.Get("entries.0.delta").Int(), 10)
		t.Assert(jled.Get("entries.0.source").String(), "checkin")

		// ── 5. Points checkout (cost 5 ≤ balance 10) → grant, balance 5 ────────
		redeemBody := `{"items":[{"siteKey":"resource","externalId":"pts-1","variantId":"points-basic","pointsCost":999,"title":"Tampered Points Resource","quantity":1}]}`
		rd, err := authC.Post(ctx, "/api/v1/checkouts/points", redeemBody)
		t.AssertNil(err)
		jrd := gjson.New(rd.ReadAllString())
		rd.Close()
		t.Assert(jrd.Get("balance").Int(), 5)

		// access for the redeemed product → entitled
		acc, err := authC.Get(ctx, "/api/v1/access?siteKey=resource&externalId=pts-1")
		t.AssertNil(err)
		jacc := gjson.New(acc.ReadAllString())
		acc.Close()
		t.Assert(jacc.Get("entitled").Bool(), true)
		t.Assert(jacc.Get("reason").String(), "ok")

		// ── 6. Redeem again → blocked by catalog purchase limit ───────────────
		rd2, err := authC.Post(ctx, "/api/v1/checkouts/points", redeemBody)
		t.AssertNil(err)
		jrd2 := gjson.New(rd2.ReadAllString())
		rd2.Close()
		t.Assert(jrd2.Get("code").String(), "commerce.notify_invalid")

		// ── 7. Unaffordable redeem → 402 insufficient_points, balance unchanged
		bigBody := `{"items":[{"siteKey":"resource","externalId":"pts-2","variantId":"points-expensive","pointsCost":1,"title":"Tampered Expensive","quantity":1}]}`
		rdx, err := authC.Post(ctx, "/api/v1/checkouts/points", bigBody)
		t.AssertNil(err)
		jrdx := gjson.New(rdx.ReadAllString())
		t.Assert(rdx.StatusCode, 402)
		rdx.Close()
		t.Assert(jrdx.Get("code").String(), "commerce.insufficient_points")

		balx, err := authC.Get(ctx, "/api/v1/credits/balance")
		t.AssertNil(err)
		t.Assert(gjson.New(balx.ReadAllString()).Get("balance").Int(), 5)
		balx.Close()

		// access for pts-2 → not entitled, reason insufficient_points + required.pointsCost
		accx, err := authC.Get(ctx, "/api/v1/access?siteKey=resource&externalId=pts-2")
		t.AssertNil(err)
		jaccx := gjson.New(accx.ReadAllString())
		accx.Close()
		t.Assert(jaccx.Get("entitled").Bool(), false)
		t.Assert(jaccx.Get("reason").String(), "insufficient_points")
		t.Assert(jaccx.Get("required.pointsCost").Int(), 9999)
	})
}
