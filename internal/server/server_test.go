package server_test

// HTTP integration test for the commerce service.
// Requires a live PostgreSQL instance: set COMMERCE_PG_HOST (and optionally
// COMMERCE_PG_PORT / COMMERCE_PG_USER / COMMERCE_PG_PASS) to run.
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
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/encoding/gjson"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/test/gtest"
	_ "github.com/lib/pq"

	"platform/gokit/authjwt"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/gateway"
	"platform/services/commerce/internal/model"
	"platform/services/commerce/internal/server"
)

// ─── test constants ──────────────────────────────────────────────────────────

const (
	testIssuer    = "http://localhost:8081"
	testKID       = "commerce-test-kid"
	testSubAlice  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	testSubBob    = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	fakePayURL    = "https://fakepay.example.com/pay?token=TEST"
	fakeSiteKey   = "resource"
	fakeExtID     = "res-test-001"
)

// ─── fake gateway ────────────────────────────────────────────────────────────

// fakeGateway is an in-memory PaymentGateway for tests.
type fakeGateway struct {
	// successBodies maps raw body bytes to the NotifyOut to return.
	// When nil, any body with the sentinel prefix triggers success.
	successBody []byte
}

// failingGateway always returns an error from CreatePayment, simulating a
// gateway outage.  Used to test the order-cancel-on-failure path.
type failingGateway struct{}

func (f *failingGateway) CreatePayment(_ context.Context, _ gateway.CreateIn) (string, error) {
	return "", fmt.Errorf("simulated gateway failure")
}

func (f *failingGateway) VerifyNotify(_ context.Context, _ []byte, _ map[string]string) (*gateway.NotifyOut, error) {
	return nil, fmt.Errorf("simulated gateway failure")
}

func (f *fakeGateway) CreatePayment(_ context.Context, in gateway.CreateIn) (string, error) {
	// Record the order number in the sentinel body so VerifyNotify can match.
	f.successBody = []byte("order=" + in.OrderNo)
	return fakePayURL, nil
}

func (f *fakeGateway) VerifyNotify(_ context.Context, body []byte, _ map[string]string) (*gateway.NotifyOut, error) {
	if f.successBody != nil && bytes.Equal(body, f.successBody) {
		// Extract order number from "order=<orderNo>"
		orderNo := string(body[len("order="):])
		return &gateway.NotifyOut{
			Success:      true,
			OrderNo:      orderNo,
			ProviderTxID: "FAKE-TX-" + orderNo,
			AmountCents:  9900, // must match what CreateOrder stores
		}, nil
	}
	return &gateway.NotifyOut{Success: false}, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func mustVerifier(t *gtest.T, priv *rsa.PrivateKey) *authjwt.Verifier {
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: priv.Public(), KeyID: testKID, Algorithm: "RS256", Use: "sig",
	}}}
	v, err := authjwt.NewVerifier(authjwt.VerifierConfig{
		Keys:   authjwt.NewStaticKeySource(set),
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
	}).CompactSerialize()
	t.AssertNil(err)
	return raw
}

var serverSeq int

func newTestServer(t *gtest.T, db *dao.PG, fake *fakeGateway, v *authjwt.Verifier, devSettle bool) *ghttp.Server {
	return newTestServerWithGW(t, db, fake, v, devSettle)
}

func newTestServerWithGW(t *gtest.T, db *dao.PG, gw gateway.PaymentGateway, v *authjwt.Verifier, devSettle bool) *ghttp.Server {
	serverSeq++
	name := fmt.Sprintf("%s-%d", t.Name(), serverSeq)
	reg := gateway.Registry{"alipay": gw}
	s := g.Server(name)
	s.SetAddr("127.0.0.1:0")
	server.Configure(s, server.Deps{
		Verifier:  v,
		DB:        db,
		Registry:  reg,
		NotifyURL: "http://localhost:8084/api/v1/payments/alipay/notify",
		ReturnURL: "http://localhost:3000/pay/return",
		DevSettle: devSettle,
	})
	s.SetDumpRouterMap(false)
	s.Start()
	return s
}

func prefix(s *ghttp.Server) string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
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

	// Ensure the commerce database exists.
	mdb, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=postgres sslmode=disable",
		host, port, user, pass))
	t.AssertNil(err)
	_, _ = mdb.Exec("CREATE DATABASE commerce")
	mdb.Close()

	// Connect to commerce and (re)apply schema.
	cdb, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=commerce sslmode=disable",
		host, port, user, pass))
	t.AssertNil(err)
	migration, err := os.ReadFile("../../manifest/sql/migrations/0001_init.up.sql")
	t.AssertNil(err)
	_, _ = cdb.Exec("DROP TABLE IF EXISTS entitlements CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS orders CASCADE")
	_, _ = cdb.Exec("DROP TABLE IF EXISTS products CASCADE")
	_, err = cdb.Exec(string(migration))
	t.AssertNil(err)
	cdb.Close()

	db, err := gdb.New(gdb.ConfigNode{
		Type: "pgsql",
		Host: host,
		Port: port,
		User: user,
		Pass: pass,
		Name: "commerce",
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

		// ── 1. No auth → 401 + common.unauthorized ────────────────────────────
		c := g.Client()
		c.SetPrefix(base)
		rawResp, err := c.Post(ctx, "/api/v1/orders", `{
			"siteKey":"resource","externalId":"res-no-auth","kind":"paid",
			"priceCents":9900,"title":"Test Article","currency":"CNY"
		}`)
		t.AssertNil(err)
		defer rawResp.Close()
		t.Assert(rawResp.StatusCode, 401)
		j := gjson.New(rawResp.ReadAllString())
		// authjwt.Middleware writes a 401 with "common.unauthorized" (before our handler)
		t.Assert(j.Get("code").String(), "common.unauthorized")

		// ── 2. CreateOrder with valid JWT → 200 + orderNo + payUrl ───────────
		authC := g.Client()
		authC.SetPrefix(base)
		authC.SetHeader("Authorization", "Bearer "+aliceToken)
		authC.SetHeader("Content-Type", "application/json")

		createBody := fmt.Sprintf(`{
			"siteKey":%q,"externalId":%q,"kind":"paid",
			"priceCents":9900,"title":"Test Article","currency":"CNY"
		}`, fakeSiteKey, fakeExtID)

		createResp, err := authC.Post(ctx, "/api/v1/orders", createBody)
		t.AssertNil(err)
		defer createResp.Close()
		t.Assert(createResp.StatusCode, 200)
		jCreate := gjson.New(createResp.ReadAllString())
		t.Assert(jCreate.Get("code").String(), "ok")
		orderNo := jCreate.Get("data.orderNo").String()
		payURL := jCreate.Get("data.payUrl").String()
		t.AssertNE(orderNo, "")
		t.Assert(payURL, fakePayURL)

		// ── 3. GET /access for unpurchased → {entitled:false, reason:not_purchased}
		accResp, err := authC.Get(ctx, fmt.Sprintf("/api/v1/access?siteKey=%s&externalId=%s", fakeSiteKey, fakeExtID))
		t.AssertNil(err)
		defer accResp.Close()
		t.Assert(accResp.StatusCode, 200)
		jAcc := gjson.New(accResp.ReadAllString())
		t.Assert(jAcc.Get("code").String(), "ok")
		t.Assert(jAcc.Get("data.entitled").Bool(), false)
		t.Assert(jAcc.Get("data.reason").String(), "not_purchased")

		// ── 4. Dev settle → order becomes paid ────────────────────────────────
		settleC := g.Client()
		settleC.SetPrefix(base)
		settleResp, err := settleC.Post(ctx, "/dev/orders/"+orderNo+"/settle", nil)
		t.AssertNil(err)
		defer settleResp.Close()
		t.Assert(settleResp.StatusCode, 200)
		jSettle := gjson.New(settleResp.ReadAllString())
		t.Assert(jSettle.Get("code").String(), "ok")

		// ── 5. GET /access now → {entitled:true, reason:ok} ──────────────────
		accResp2, err := authC.Get(ctx, fmt.Sprintf("/api/v1/access?siteKey=%s&externalId=%s", fakeSiteKey, fakeExtID))
		t.AssertNil(err)
		defer accResp2.Close()
		t.Assert(accResp2.StatusCode, 200)
		jAcc2 := gjson.New(accResp2.ReadAllString())
		t.Assert(jAcc2.Get("code").String(), "ok")
		t.Assert(jAcc2.Get("data.entitled").Bool(), true)
		t.Assert(jAcc2.Get("data.reason").String(), "ok")

		// ── 6. Notify idempotency ─────────────────────────────────────────────
		// Create a second order for a fresh product to test notify idempotency.
		notifyExtID := "res-notify-idem-001"
		createBody2 := fmt.Sprintf(`{
			"siteKey":%q,"externalId":%q,"kind":"paid",
			"priceCents":9900,"title":"Notify Test","currency":"CNY"
		}`, fakeSiteKey, notifyExtID)
		createResp2, err := authC.Post(ctx, "/api/v1/orders", createBody2)
		t.AssertNil(err)
		defer createResp2.Close()
		t.Assert(createResp2.StatusCode, 200)
		jCreate2 := gjson.New(createResp2.ReadAllString())
		orderNo2 := jCreate2.Get("data.orderNo").String()
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
		t.Assert(jAcc3.Get("data.entitled").Bool(), true)
		t.Assert(jAcc3.Get("data.reason").String(), "ok")

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

// TestCreateOrder_InputValidation verifies that over-long Title or non-CNY
// currency returns 400 (not 502 or 200).
func TestCreateOrder_InputValidation(t *testing.T) {
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

		cases := []struct {
			name string
			body string
		}{
			{
				name: "title too long (>200 chars)",
				body: fmt.Sprintf(`{"siteKey":"resource","externalId":"val-001","kind":"paid","priceCents":100,"title":%q,"currency":"CNY"}`,
					string(make([]byte, 201))),
			},
			{
				name: "non-CNY currency",
				body: `{"siteKey":"resource","externalId":"val-002","kind":"paid","priceCents":100,"title":"T","currency":"USD"}`,
			},
			{
				name: "empty currency",
				body: `{"siteKey":"resource","externalId":"val-003","kind":"paid","priceCents":100,"title":"T","currency":""}`,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.T.Run(tc.name, func(tt *testing.T) {
				resp, err := authC.Post(ctx, "/api/v1/orders", tc.body)
				if err != nil {
					tt.Fatalf("POST: %v", err)
				}
				defer resp.Close()
				if resp.StatusCode != 400 {
					tt.Errorf("status = %d, want 400 for %q", resp.StatusCode, tc.name)
				}
			})
		}
	})
}

// TestCreateOrder_GatewayFailure_OrderCancelled verifies that when the payment
// gateway's CreatePayment returns an error, the pending order is transitioned
// to "cancelled" rather than left as an orphan.
func TestCreateOrder_GatewayFailure_OrderCancelled(t *testing.T) {
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

		createBody := `{"siteKey":"resource","externalId":"gw-fail-001","kind":"paid","priceCents":9900,"title":"Test","currency":"CNY"}`
		resp, err := authC.Post(ctx, "/api/v1/orders", createBody)
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
		rawDB, err2 := sql.Open("postgres", fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=commerce sslmode=disable",
			host, port, user, pass))
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
