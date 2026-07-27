package alipay_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/yueli-official/commerce/paykit"
	"github.com/yueli-official/commerce/paykit/providers/alipay"
)

// generateTestRSAKey produces a throwaway RSA-2048 key pair for offline tests.
// The public key is returned in the bare-base64 format that alipay.LoadAliPayPublicKey expects.
func generateTestRSAKey(t *testing.T) (privateKeyBase64, publicKeyBase64 string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	// Private key → PKCS8 DER → PEM → strip headers → bare base64.
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	privateKeyBase64 = strippedBase64(privPEM)

	// Public key → PKIX DER → PEM → strip headers → bare base64.
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	publicKeyBase64 = strippedBase64(pubPEM)

	return
}

// strippedBase64 removes PEM headers/footers and newlines, leaving bare base64.
func strippedBase64(pemBytes []byte) string {
	s := string(pemBytes)
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "")
}

// TestRegistry_RegisterAndRetrieve verifies the Registry map pattern:
// register "alipay" → retrieve it back by slug.
func TestRegistry_RegisterAndRetrieve(t *testing.T) {
	privKey, pubKey := generateTestRSAKey(t)

	cfg := alipay.Config{
		AppID:           "test_app_id",
		PrivateKey:      privKey,
		AlipayPublicKey: pubKey,
		Sandbox:         true,
	}
	gw, err := alipay.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	reg := paykit.NewRegistry()
	if err := reg.Register(gw); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := reg["alipay"]
	if !ok {
		t.Fatal("expected to find 'alipay' in registry")
	}
	if got != gw {
		t.Fatal("retrieved gateway does not match registered one")
	}
}

// TestVerifyNotify_InvalidSignature verifies that a tampered/empty body
// causes VerifyNotify to return an error (not panic).
func TestVerifyNotify_InvalidSignature(t *testing.T) {
	privKey, pubKey := generateTestRSAKey(t)

	cfg := alipay.Config{
		AppID:           "test_app_id",
		PrivateKey:      privKey,
		AlipayPublicKey: pubKey,
		Sandbox:         true,
	}
	gw, err := alipay.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	cases := []struct {
		name string
		body []byte
	}{
		{"empty body", []byte("")},
		{"garbage body", []byte("not=valid&form=body")},
		{"tampered sign", []byte("trade_status=TRADE_SUCCESS&out_trade_no=12345&sign=" + base64.StdEncoding.EncodeToString([]byte("invalid_signature")))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gw.VerifyNotify(context.Background(), tc.body, nil)
			if err == nil {
				t.Error("expected error for invalid/tampered body, got nil")
			}
		})
	}
}

// TestCheckNotifyAppID_Mismatch verifies that checkNotifyAppID rejects a
// notification whose app_id does not match the configured merchant app_id.
// This exercises the defense-in-depth guard in VerifyNotify without requiring
// a fully-signed payload (constructing such a payload offline is impractical).
func TestCheckNotifyAppID_Mismatch(t *testing.T) {
	err := alipay.CheckNotifyAppID("attacker_app_id", "merchant_app_id")
	if err == nil {
		t.Fatal("expected error for mismatched app_id, got nil")
	}
	if !strings.Contains(err.Error(), "app_id mismatch") {
		t.Errorf("error message %q does not mention app_id mismatch", err.Error())
	}
}

// TestCheckNotifyAppID_Match verifies that checkNotifyAppID accepts a matching app_id.
func TestCheckNotifyAppID_Match(t *testing.T) {
	if err := alipay.CheckNotifyAppID("my_app_id", "my_app_id"); err != nil {
		t.Fatalf("expected nil for matching app_id, got %v", err)
	}
}

// TestCreatePayment_ProducesRedirectURL constructs an alipay provider with a
// throwaway RSA key (no real Alipay creds), calls CreatePayment, and asserts
// the returned URL is non-empty and points to an Alipay gateway host.
// This exercises the real sign path offline without contacting Alipay's servers.
func TestCreatePayment_ProducesRedirectURL(t *testing.T) {
	privKey, pubKey := generateTestRSAKey(t)

	cfg := alipay.Config{
		AppID:           "2021000117628050", // synthetic but structurally valid
		PrivateKey:      privKey,
		AlipayPublicKey: pubKey,
		Sandbox:         true, // points to sandbox host; no network call needed for page-pay
	}
	gw, err := alipay.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	in := paykit.CreatePaymentIn{
		OrderNo:     "TEST-ORDER-001",
		Subject:     "Test Product",
		AmountCents: 9900, // ¥99.00
		NotifyURL:   "https://example.com/notify",
		ReturnURL:   "https://example.com/return",
	}

	payment, err := gw.CreatePayment(context.Background(), in)
	if err != nil {
		t.Fatalf("CreatePayment error: %v", err)
	}
	if payment.PayURL == "" {
		t.Fatal("expected non-empty payURL")
	}
	if payment.Method != string(paykit.CapabilityRedirect) {
		t.Fatalf("method = %q, want redirect", payment.Method)
	}
	// Alipay sandbox host is openapi.alipaydev.com; production is openapi.alipay.com.
	if !strings.Contains(payment.PayURL, "alipay") {
		t.Errorf("payURL %q does not contain expected alipay gateway host", payment.PayURL)
	}
	t.Logf("CreatePayment offline URL (first 120 chars): %.120s...", payment.PayURL)
}
