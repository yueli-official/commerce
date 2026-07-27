package appconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yueli-official/foundation/go/capability"
	"github.com/yueli-official/notification/client"
	"github.com/yueli-official/commerce/paykit"
	paydev "github.com/yueli-official/commerce/paykit/providers/dev"
	"platform/services/commerce/internal/paymentcap"
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

func TestBuildPaymentCapabilityRegistryIncludesRegisteredDevProvider(t *testing.T) {
	gateways := paykit.NewRegistry()
	if err := gateways.Register(paydev.NewProvider()); err != nil {
		t.Fatal(err)
	}
	registry, err := BuildPaymentCapabilityRegistry(gateways, Alipay{}, PayPal{}, WeChat{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(
		capability.ServiceMetadata{Name: "commerce", Version: "test", BuildSHA: "test", Deployment: "commerce-test"},
		[]paymentcap.MethodState{{Provider: "dev", Enabled: true}},
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := snapshot.Provider("dev-local")
	if !ok || !provider.Registered || provider.Mode != "local" ||
		provider.Configuration != capability.ConfigurationComplete ||
		provider.Enablement != capability.EnablementEnabled {
		t.Fatalf("dev provider = %+v, present=%t", provider, ok)
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
