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
		saw = true
		if r.URL.Path != "/api/v1/notifications/send" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Notification-Token"); got != "secret" {
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

	cli, err := notificationclient.New(notificationclient.Config{BaseURL: srv.URL, APIToken: "secret"})
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
