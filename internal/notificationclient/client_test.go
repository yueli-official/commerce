package notificationclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"platform/services/commerce/internal/notificationclient"
)

func TestClientSendsNotificationEnvelope(t *testing.T) {
	var saw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("client_id") != "commerce-notification-svc" || r.Form.Get("client_secret") != "secret" || r.Form.Get("scope") != "notification:send" {
				t.Fatalf("unexpected token request: %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "notification-token"})
			return
		}
		saw = true
		if r.URL.Path != "/api/v1/notifications/send" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer notification-token" {
			t.Fatalf("token header = %q", got)
		}
		var body notificationclient.SendInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Scene != "commerce.delivery_ready" || body.Recipient.Email != "buyer@example.com" {
			t.Fatalf("unexpected request body: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "ok",
			"data": map[string]any{"messageId": "msg-1", "status": "sent", "provider": "dev"},
		})
	}))
	defer srv.Close()

	cli, err := notificationclient.New(notificationclient.Config{BaseURL: srv.URL, TokenURL: srv.URL + "/oauth2/token", ClientID: "commerce-notification-svc", ClientSecret: "secret", Scope: "notification:send"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := cli.Send(context.Background(), notificationclient.SendInput{
		IdempotencyKey: "key-1",
		Scene:          "commerce.delivery_ready",
		Channel:        "email",
		Recipient:      notificationclient.Recipient{Email: "buyer@example.com"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !saw || out.MessageID != "msg-1" || out.Status != "sent" {
		t.Fatalf("unexpected output: saw=%v out=%+v", saw, out)
	}
}
