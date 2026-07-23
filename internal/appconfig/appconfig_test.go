package appconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"platform/gokit/capability"
	"platform/gokit/notificationclient"
	"platform/paykit"
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

func TestBuildPaymentCapabilityRegistryReportsOmittedConfig(t *testing.T) {
	registry, err := BuildPaymentCapabilityRegistry(paykit.NewRegistry(), Alipay{}, PayPal{}, WeChat{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(
		capability.ServiceMetadata{Name: "commerce", Version: "test", BuildSHA: "test", Deployment: "commerce-test"},
		nil,
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"alipay-primary", "paypal-primary", "wechat-primary"} {
		provider, ok := snapshot.Provider(key)
		if !ok || provider.Configuration != capability.ConfigurationMissing || provider.Enablement != capability.EnablementDisabled || provider.Health != capability.HealthUnknown || provider.Effective {
			t.Fatalf("provider %s = %+v, %t", key, provider, ok)
		}
	}
}
