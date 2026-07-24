package assetclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"platform/services/commerce/internal/assetclient"
)

func TestClientCreateDeliveryMintsAssetGrant(t *testing.T) {
	var sawToken, sawGrant bool
	expires := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			sawToken = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "asset:sign" {
				t.Fatalf("unexpected token form: %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "ASSET-TOKEN"})
		case "/api/v1/delivery-grants":
			w.Header().Set("Content-Type", "application/json")
			sawGrant = true
			if got := r.Header.Get("Authorization"); got != "Bearer ASSET-TOKEN" {
				t.Fatalf("authorization = %q, want bearer token", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode grant body: %v", err)
			}
			if body["assetId"] != "asset-123" || body["subjectId"] != "buyer@example.com" {
				t.Fatalf("unexpected grant body: %+v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"grantId": "grant-123", "url": "https://asset.example/grants/TOKEN", "expiresAt": expires})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cli, err := assetclient.New(assetclient.Config{
		BaseURL: srv.URL, TokenURL: srv.URL + "/oauth2/token", ClientID: "commerce", ClientSecret: "secret",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := cli.CreateDelivery(context.Background(), assetclient.DeliveryInput{
		AssetID: "asset-123", SubjectID: "buyer@example.com", ExpiresIn: 300, Reason: "commerce:ORDER-1",
	})
	if err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	if !sawToken || !sawGrant {
		t.Fatalf("expected token and grant endpoints to be called")
	}
	if out.URL != "https://asset.example/grants/TOKEN" {
		t.Fatalf("url = %q", out.URL)
	}
	if out.GrantID != "grant-123" {
		t.Fatalf("grant id = %q", out.GrantID)
	}
	if out.ExpiresAt.IsZero() {
		t.Fatal("expected parsed expiry")
	}
}

func TestClientRevokeDeliveryTreatsAlreadyRevokedAsConverged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "ASSET-TOKEN"})
		case "/api/v1/admin/assets/grants/grant-123/revoke":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer ASSET-TOKEN" {
				t.Fatalf("authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]bool{"revoked": false})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	client, err := assetclient.New(assetclient.Config{
		BaseURL: srv.URL, TokenURL: srv.URL + "/oauth2/token",
		ClientID: "commerce", ClientSecret: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RevokeDelivery(context.Background(), "grant-123"); err != nil {
		t.Fatalf("RevokeDelivery: %v", err)
	}
}
