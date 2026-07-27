package paymentcap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yueli-official/foundation/go/capability"
	"platform/paykit"
)

type healthGateway struct {
	*paykit.FakeProvider
	healthErr   error
	healthCalls int
}

func (gateway *healthGateway) CheckHealth(context.Context) error {
	gateway.healthCalls++
	return gateway.healthErr
}

func TestSnapshotSeparatesEnablementConfigurationHealthAndMode(t *testing.T) {
	alipay := &healthGateway{FakeProvider: paykit.NewFakeProvider("alipay")}
	registry, err := New(
		Definition{Instance: "alipay-primary", Adapter: "alipay", Mode: "sandbox", Gateway: alipay, Operations: []string{"query", "redirect"}, RequiredConfig: []capability.ConfigField{
			{Key: "app_id", State: capability.ConfigStatePresent}, {Key: "private_key", State: capability.ConfigStatePresent, Secret: true},
		}},
		Definition{Instance: "paypal-primary", Adapter: "paypal", Mode: "production", Operations: []string{"browser_button", "refund"}, RequiredConfig: []capability.ConfigField{
			{Key: "client_id", State: capability.ConfigStatePresent}, {Key: "client_secret", State: capability.ConfigStateMissing, Secret: true},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	methods := []MethodState{{Provider: "alipay", Enabled: true}, {Provider: "paypal", Enabled: false}}
	snapshot, err := registry.Snapshot(testMetadata(), methods, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	alipayView, _ := snapshot.Provider("alipay-primary")
	if !alipayView.Registered || alipayView.Configuration != capability.ConfigurationComplete || alipayView.Enablement != capability.EnablementEnabled || alipayView.Health != capability.HealthUnknown || alipayView.Effective || alipayView.Mode != "sandbox" {
		t.Fatalf("alipay provider = %+v", alipayView)
	}
	paypalView, _ := snapshot.Provider("paypal-primary")
	if paypalView.Registered || paypalView.Configuration != capability.ConfigurationPartial || paypalView.Enablement != capability.EnablementDisabled || paypalView.Effective {
		t.Fatalf("paypal provider = %+v", paypalView)
	}
	if err := registry.CheckHealth(context.Background(), "alipay-primary"); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = registry.Snapshot(testMetadata(), methods, time.Now())
	alipayView, _ = snapshot.Provider("alipay-primary")
	payment, _ := snapshot.Capability("commerce.payment")
	if !alipayView.Effective || !payment.Effective || alipay.healthCalls != 1 {
		t.Fatalf("healthy provider/capability = %+v / %+v calls=%d", alipayView, payment, alipay.healthCalls)
	}
}

func TestHealthCheckNeverCallsPaymentOperations(t *testing.T) {
	probeErr := errors.New("credentials rejected")
	gateway := &healthGateway{FakeProvider: paykit.NewFakeProvider("paypal"), healthErr: probeErr}
	registry, err := New(Definition{Instance: "paypal-primary", Adapter: "paypal", Gateway: gateway})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.CheckHealth(context.Background(), "paypal-primary"); !errors.Is(err, probeErr) {
		t.Fatalf("CheckHealth() error = %v", err)
	}
	if len(gateway.CreateCalls) != 0 || len(gateway.CaptureCalls) != 0 || len(gateway.RefundCalls) != 0 {
		t.Fatalf("health check mutated payments: %+v", gateway)
	}
}

func TestManifestRedactsPaymentCredentials(t *testing.T) {
	registry, err := New(Definition{Instance: "paypal-primary", Adapter: "paypal", RequiredConfig: []capability.ConfigField{
		{Key: "client_id", State: capability.ConfigStatePresent}, {Key: "client_secret", State: capability.ConfigStatePresent, Secret: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(testMetadata(), []MethodState{{Provider: "paypal", Enabled: true}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(snapshot.Manifest())
	for _, forbidden := range []string{"secretValue", "client-secret-value", "passwordValue"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("manifest leaked %q: %s", forbidden, data)
		}
	}
}

func TestAggregateUsesOneEnabledProviderPath(t *testing.T) {
	configured := &healthGateway{FakeProvider: paykit.NewFakeProvider("alipay")}
	partial := &healthGateway{FakeProvider: paykit.NewFakeProvider("paypal")}
	registry, err := New(
		Definition{Instance: "alipay-primary", Adapter: "alipay", Gateway: configured, RequiredConfig: []capability.ConfigField{{Key: "app_id", State: capability.ConfigStatePresent}}},
		Definition{Instance: "paypal-primary", Adapter: "paypal", Gateway: partial, RequiredConfig: []capability.ConfigField{{Key: "client_id", State: capability.ConfigStatePresent}, {Key: "client_secret", State: capability.ConfigStateMissing, Secret: true}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(testMetadata(), []MethodState{{Provider: "alipay", Enabled: false}, {Provider: "paypal", Enabled: true}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payment, _ := snapshot.Capability("commerce.payment")
	if payment.Configuration != capability.ConfigurationPartial || payment.Enablement != capability.EnablementEnabled {
		t.Fatalf("aggregate mixed provider paths: %+v", payment)
	}
}

func TestProviderDimensionsVaryIndependently(t *testing.T) {
	partialGateway := &healthGateway{FakeProvider: paykit.NewFakeProvider("alipay")}
	disabledGateway := &healthGateway{FakeProvider: paykit.NewFakeProvider("wechat")}
	registry, err := New(
		Definition{Instance: "paypal-primary", Adapter: "paypal", RequiredConfig: []capability.ConfigField{{Key: "client_id", State: capability.ConfigStatePresent}}},
		Definition{Instance: "alipay-primary", Adapter: "alipay", Gateway: partialGateway, RequiredConfig: []capability.ConfigField{{Key: "app_id", State: capability.ConfigStatePresent}, {Key: "private_key", State: capability.ConfigStateMissing, Secret: true}}},
		Definition{Instance: "wechat-primary", Adapter: "wechat", Gateway: disabledGateway, RequiredConfig: []capability.ConfigField{{Key: "merchant_id", State: capability.ConfigStatePresent}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	methods := []MethodState{{Provider: "paypal", Enabled: true}, {Provider: "alipay", Enabled: true}, {Provider: "wechat", Enabled: false}}
	if err := registry.CheckHealth(context.Background(), "wechat-primary"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(testMetadata(), methods, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	unregistered, _ := snapshot.Provider("paypal-primary")
	if unregistered.Registered || unregistered.Configuration != capability.ConfigurationComplete || unregistered.Enablement != capability.EnablementEnabled || unregistered.Effective {
		t.Fatalf("unregistered dimension collapsed: %+v", unregistered)
	}
	partialView, _ := snapshot.Provider("alipay-primary")
	if !partialView.Registered || partialView.Configuration != capability.ConfigurationPartial || partialView.Enablement != capability.EnablementEnabled || partialView.Effective {
		t.Fatalf("configured dimension collapsed: %+v", partialView)
	}
	disabled, _ := snapshot.Provider("wechat-primary")
	if !disabled.Registered || disabled.Configuration != capability.ConfigurationComplete || disabled.Enablement != capability.EnablementDisabled || disabled.Health != capability.HealthHealthy || disabled.Effective {
		t.Fatalf("enablement/health dimensions collapsed: %+v", disabled)
	}
}

func testMetadata() capability.ServiceMetadata {
	return capability.ServiceMetadata{Name: "commerce", Version: "test", BuildSHA: "test", Deployment: "commerce-test"}
}
