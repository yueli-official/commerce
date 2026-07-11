package appconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"platform/services/commerce/internal/notificationclient"
	"platform/services/commerce/internal/service"
)

func TestNotificationDeliverySenderSwallowsProviderFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "notification-token"})
			return
		}
		if r.URL.Path != "/api/v1/notifications/send" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "notification.provider_failed",
			"message": "notification provider failed",
		})
	}))
	defer srv.Close()

	client, err := notificationclient.New(notificationclient.Config{BaseURL: srv.URL, TokenURL: srv.URL + "/oauth2/token", ClientID: "commerce-notification-svc", ClientSecret: "secret"})
	if err != nil {
		t.Fatalf("New notification client: %v", err)
	}
	sender := notificationDeliverySender{client: client}

	err = sender.SendDelivery(context.Background(), service.DeliveryMail{
		To:          "buyer@example.com",
		OrderNo:     "ORDER-1",
		Title:       "Design Pack",
		DeliveryRef: "asset-1",
		DeliveryURL: "https://example.test/d",
	})
	if err != nil {
		t.Fatalf("SendDelivery returned error: %v", err)
	}
}
