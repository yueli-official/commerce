package paypal_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yueli-official/commerce/paykit"
	"github.com/yueli-official/commerce/paykit/providers/paypal"
)

func TestPayPalCreatePaymentCreatesOrder(t *testing.T) {
	var sawToken, sawCreate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth2/token":
			sawToken = true
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s, want POST", r.Method)
			}
			auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
			decoded, err := base64.StdEncoding.DecodeString(auth)
			if err != nil {
				t.Fatalf("decode basic auth: %v", err)
			}
			if string(decoded) != "client:secret" {
				t.Fatalf("basic auth = %q, want client:secret", decoded)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			if r.Form.Get("grant_type") != "client_credentials" {
				t.Fatalf("grant_type = %q, want client_credentials", r.Form.Get("grant_type"))
			}
			writeJSON(t, w, map[string]any{"access_token": "token-1", "token_type": "Bearer"})
		case "/v2/checkout/orders":
			sawCreate = true
			if r.Method != http.MethodPost {
				t.Fatalf("create method = %s, want POST", r.Method)
			}
			if r.Header.Get("Authorization") != "Bearer token-1" {
				t.Fatalf("authorization = %q, want bearer token", r.Header.Get("Authorization"))
			}
			var body struct {
				Intent        string `json:"intent"`
				PurchaseUnits []struct {
					ReferenceID string `json:"reference_id"`
					Amount      struct {
						CurrencyCode string `json:"currency_code"`
						Value        string `json:"value"`
					} `json:"amount"`
				} `json:"purchase_units"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body.Intent != "CAPTURE" {
				t.Fatalf("intent = %q, want CAPTURE", body.Intent)
			}
			if len(body.PurchaseUnits) != 1 {
				t.Fatalf("purchase units = %d, want 1", len(body.PurchaseUnits))
			}
			unit := body.PurchaseUnits[0]
			if unit.ReferenceID != "ORD-PP-1" {
				t.Fatalf("reference id = %q, want order number", unit.ReferenceID)
			}
			if unit.Amount.CurrencyCode != "USD" || unit.Amount.Value != "12.34" {
				t.Fatalf("amount = %s %s, want USD 12.34", unit.Amount.CurrencyCode, unit.Amount.Value)
			}
			writeJSON(t, w, map[string]any{"id": "PAYPAL-ORDER-1", "status": "CREATED"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	gw, err := paypal.NewProvider(paypal.Config{
		ClientID:     "client",
		ClientSecret: "secret",
		BaseURL:      srv.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	out, err := gw.CreatePayment(context.Background(), paykit.CreatePaymentIn{
		OrderNo:     "ORD-PP-1",
		Subject:     "Digital product",
		AmountCents: 1234,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if !sawToken || !sawCreate {
		t.Fatalf("expected token and create calls, got token=%v create=%v", sawToken, sawCreate)
	}
	if out.Provider != "paypal" || out.Method != string(paykit.CapabilityBrowserButton) {
		t.Fatalf("unexpected provider/method: %+v", out)
	}
	if out.SessionID != "PAYPAL-ORDER-1" || out.ClientToken != "PAYPAL-ORDER-1" {
		t.Fatalf("unexpected session/client token: %+v", out)
	}
}

func TestPayPalHealthCheckOnlyRequestsOAuthToken(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/v1/oauth2/token" || request.Method != http.MethodPost {
			t.Fatalf("health request = %s %s", request.Method, request.URL.Path)
		}
		writeJSON(t, w, map[string]any{"access_token": "health-token"})
	}))
	defer server.Close()
	gateway, err := paypal.NewProvider(paypal.Config{ClientID: "client", ClientSecret: "secret", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	checker, ok := gateway.(paykit.HealthChecker)
	if !ok {
		t.Fatal("paypal provider does not implement HealthChecker")
	}
	if err := checker.CheckHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("health requests = %d, want 1", requests)
	}
}

func TestPayPalCapturePaymentCapturesApprovedOrder(t *testing.T) {
	srv := paypalTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/checkout/orders/PAYPAL-ORDER-1/capture" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("capture method = %s, want POST", r.Method)
		}
		writeJSON(t, w, map[string]any{
			"id":     "PAYPAL-ORDER-1",
			"status": "COMPLETED",
			"purchase_units": []map[string]any{{
				"payments": map[string]any{
					"captures": []map[string]any{{
						"id":     "CAPTURE-1",
						"status": "COMPLETED",
						"amount": map[string]any{"currency_code": "USD", "value": "12.34"},
					}},
				},
			}},
		})
	})
	defer srv.Close()

	gw := newPayPalTestProvider(t, srv.URL)
	out, err := gw.CapturePayment(context.Background(), paykit.CapturePaymentIn{
		OrderNo:     "ORD-PP-1",
		SessionID:   "PAYPAL-ORDER-1",
		AmountCents: 1234,
	})
	if err != nil {
		t.Fatalf("CapturePayment: %v", err)
	}
	if !out.Success || out.OrderNo != "ORD-PP-1" || out.ProviderTxID != "CAPTURE-1" || out.AmountCents != 1234 {
		t.Fatalf("unexpected capture output: %+v", out)
	}
}

func TestPayPalRefundPaymentUsesCaptureRefund(t *testing.T) {
	srv := paypalTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/payments/captures/CAPTURE-1/refund" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("refund method = %s, want POST", r.Method)
		}
		var body struct {
			Amount struct {
				CurrencyCode string `json:"currency_code"`
				Value        string `json:"value"`
			} `json:"amount"`
			NoteToPayer string `json:"note_to_payer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode refund body: %v", err)
		}
		if body.Amount.CurrencyCode != "USD" || body.Amount.Value != "12.34" {
			t.Fatalf("refund amount = %s %s, want USD 12.34", body.Amount.CurrencyCode, body.Amount.Value)
		}
		if body.NoteToPayer != "customer request" {
			t.Fatalf("refund note = %q", body.NoteToPayer)
		}
		if r.Header.Get("PayPal-Request-Id") != "refund-request-1" {
			t.Fatalf("PayPal-Request-Id = %q", r.Header.Get("PayPal-Request-Id"))
		}
		writeJSON(t, w, map[string]any{"id": "REFUND-1", "status": "COMPLETED"})
	})
	defer srv.Close()

	gw := newPayPalTestProvider(t, srv.URL)
	out, err := gw.Refund(context.Background(), paykit.RefundIn{
		OrderNo: "ORD-PP-1", RefundNo: "REFUND-PP-1",
		ProviderTxID: "CAPTURE-1", AmountCents: 1234,
		TotalAmountCents: 2000, Currency: "USD", Reason: "customer request",
		IdempotencyKey: "refund-request-1",
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if !out.Success || out.ProviderID != "REFUND-1" || out.AmountCents != 1234 {
		t.Fatalf("unexpected refund output: %+v", out)
	}
}

func TestPayPalQueryRefundMapsPendingRefund(t *testing.T) {
	srv := paypalTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != "/v2/payments/refunds/REFUND-1" {
			t.Fatalf("refund query request = %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"id": "REFUND-1", "status": "PENDING",
			"amount": map[string]any{"currency_code": "USD", "value": "12.34"},
		})
	})
	defer srv.Close()

	gw := newPayPalTestProvider(t, srv.URL)
	queryGW, ok := gw.(paykit.QueryRefundProvider)
	if !ok {
		t.Fatal("paypal provider does not implement QueryRefundProvider")
	}
	out, err := queryGW.QueryRefund(context.Background(), paykit.QueryRefundIn{
		OrderNo: "ORD-PP-1", RefundNo: "REFUND-PP-1",
		ProviderRefundID: "REFUND-1", AmountCents: 1234, Currency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Success || out.Status != paykit.RefundStatusPending ||
		out.ProviderID != "REFUND-1" || out.AmountCents != 1234 ||
		out.Currency != "USD" || out.ProviderStatus != "PENDING" {
		t.Fatalf("refund query output = %+v", out)
	}
}

func TestPayPalVerifiesAndMapsDisputeWebhook(t *testing.T) {
	srv := paypalTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost ||
			r.URL.Path != "/v1/notifications/verify-webhook-signature" {
			t.Fatalf("verification request = %s %s", r.Method, r.URL.Path)
		}
		var verification struct {
			WebhookID    string         `json:"webhook_id"`
			WebhookEvent map[string]any `json:"webhook_event"`
		}
		if err := json.NewDecoder(r.Body).Decode(&verification); err != nil {
			t.Fatal(err)
		}
		if verification.WebhookID != "WH-1" ||
			verification.WebhookEvent["id"] != "WH-EVENT-1" {
			t.Fatalf("verification body = %+v", verification)
		}
		writeJSON(t, w, map[string]string{"verification_status": "SUCCESS"})
	})
	defer srv.Close()

	gw, err := paypal.NewProvider(paypal.Config{
		ClientID: "client", ClientSecret: "secret",
		BaseURL: srv.URL, WebhookID: "WH-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier, ok := gw.(paykit.VerifyDisputeProvider)
	if !ok {
		t.Fatal("paypal provider does not implement VerifyDisputeProvider")
	}
	body := []byte(`{
		"id":"WH-EVENT-1",
		"event_type":"CUSTOMER.DISPUTE.CREATED",
		"create_time":"2026-07-24T18:00:00Z",
		"resource":{
			"dispute_id":"PP-D-1",
			"status":"WAITING_FOR_SELLER_RESPONSE",
			"reason":"MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
			"create_time":"2026-07-24T17:55:00Z",
			"update_time":"2026-07-24T18:00:00Z",
			"seller_response_due_date":"2026-07-31T18:00:00Z",
			"dispute_amount":{"currency_code":"USD","value":"12.34"},
			"disputed_transactions":[{
				"seller_transaction_id":"CAPTURE-1"
			}]
		}
	}`)
	out, err := verifier.VerifyDispute(
		context.Background(),
		body,
		map[string]string{
			"Paypal-Auth-Algo":         "SHA256withRSA",
			"Paypal-Cert-Url":          "https://api.paypal.com/cert.pem",
			"Paypal-Transmission-Id":   "transmission-1",
			"Paypal-Transmission-Sig":  "signature-1",
			"Paypal-Transmission-Time": "2026-07-24T18:00:00Z",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if out.EventID != "WH-EVENT-1" ||
		out.ProviderDisputeID != "PP-D-1" ||
		out.ProviderTxID != "CAPTURE-1" ||
		out.Status != paykit.DisputeStatusNeedsResponse ||
		out.AmountCents != 1234 || out.Currency != "USD" {
		t.Fatalf("dispute output = %+v", out)
	}
}

func TestPayPalQueryDisputeMapsBuyerFavourToLost(t *testing.T) {
	srv := paypalTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != "/v1/customer/disputes/PP-D-1" {
			t.Fatalf("dispute query = %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"dispute_id": "PP-D-1", "status": "RESOLVED",
			"dispute_outcome": map[string]string{
				"outcome_code": "RESOLVED_BUYER_FAVOUR",
			},
			"dispute_amount": map[string]string{
				"currency_code": "USD", "value": "12.34",
			},
			"disputed_transactions": []map[string]string{{
				"seller_transaction_id": "CAPTURE-1",
			}},
			"update_time": "2026-07-24T18:00:00Z",
		})
	})
	defer srv.Close()

	gw := newPayPalTestProvider(t, srv.URL)
	query, ok := gw.(paykit.QueryDisputeProvider)
	if !ok {
		t.Fatal("paypal provider does not implement QueryDisputeProvider")
	}
	out, err := query.QueryDispute(context.Background(), "PP-D-1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != paykit.DisputeStatusLost ||
		out.OutcomeCode != "RESOLVED_BUYER_FAVOUR" ||
		out.ProviderTxID != "CAPTURE-1" {
		t.Fatalf("dispute query output = %+v", out)
	}
}

func TestPayPalCaptureRejectsAmountMismatch(t *testing.T) {
	srv := paypalTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"id":     "PAYPAL-ORDER-1",
			"status": "COMPLETED",
			"purchase_units": []map[string]any{{
				"payments": map[string]any{
					"captures": []map[string]any{{
						"id":     "CAPTURE-1",
						"status": "COMPLETED",
						"amount": map[string]any{"currency_code": "USD", "value": "10.00"},
					}},
				},
			}},
		})
	})
	defer srv.Close()

	gw := newPayPalTestProvider(t, srv.URL)
	_, err := gw.CapturePayment(context.Background(), paykit.CapturePaymentIn{
		OrderNo:     "ORD-PP-1",
		SessionID:   "PAYPAL-ORDER-1",
		AmountCents: 1234,
	})
	if err == nil {
		t.Fatal("expected amount mismatch error")
	}
}

func TestPayPalQueryPaymentMapsCompletedOrder(t *testing.T) {
	srv := paypalTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/checkout/orders/PAYPAL-ORDER-1" {
			t.Fatalf("query request = %s %s", r.Method, r.URL.Path)
		}
		if r.ContentLength > 0 {
			t.Fatalf("GET query content length = %d", r.ContentLength)
		}
		writeJSON(t, w, map[string]any{
			"id": "PAYPAL-ORDER-1", "status": "COMPLETED",
			"purchase_units": []map[string]any{{
				"reference_id": "ORD-PP-1",
				"amount":       map[string]any{"currency_code": "USD", "value": "12.34"},
				"payments": map[string]any{
					"captures": []map[string]any{{
						"id": "CAPTURE-1", "status": "COMPLETED",
						"amount": map[string]any{"currency_code": "USD", "value": "12.34"},
					}},
				},
			}},
		})
	})
	defer srv.Close()

	gw := newPayPalTestProvider(t, srv.URL)
	queryGW, ok := gw.(paykit.QueryPaymentProvider)
	if !ok {
		t.Fatal("paypal provider does not implement QueryPaymentProvider")
	}
	out, err := queryGW.QueryPayment(context.Background(), paykit.QueryPaymentIn{
		OrderNo: "ORD-PP-1", SessionID: "PAYPAL-ORDER-1",
		AmountCents: 1234, Currency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Success || out.Status != paykit.PaymentStatusSettled ||
		out.ProviderTxID != "CAPTURE-1" || out.AmountCents != 1234 ||
		out.Currency != "USD" || out.ProviderStatus != "COMPLETED" {
		t.Fatalf("query output = %+v", out)
	}
}

func paypalTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth2/token":
			writeJSON(t, w, map[string]any{"access_token": "token-1", "token_type": "Bearer"})
		default:
			if r.Header.Get("Authorization") != "Bearer token-1" {
				t.Fatalf("authorization = %q, want bearer token", r.Header.Get("Authorization"))
			}
			handler(w, r)
		}
	}))
}

func newPayPalTestProvider(t *testing.T, baseURL string) paykit.Provider {
	t.Helper()
	gw, err := paypal.NewProvider(paypal.Config{
		ClientID:     "client",
		ClientSecret: "secret",
		BaseURL:      baseURL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return gw
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
