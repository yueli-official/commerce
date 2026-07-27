package main

import (
	"reflect"
	"testing"

	"platform/paykit"
	"platform/services/commerce/internal/appconfig"
)

func TestBuildGatewayRegistryDoesNotRegisterStubsForDevSettle(t *testing.T) {
	reg, err := buildGatewayRegistry(
		false,
		appconfig.Alipay{},
		appconfig.PayPal{},
		appconfig.WeChat{},
	)
	if err != nil {
		t.Fatalf("buildGatewayRegistry: %v", err)
	}
	if len(reg) != 0 {
		t.Fatalf("registry len = %d, want 0 without real provider credentials", len(reg))
	}
}

func TestBuildGatewayRegistryRegistersExplicitDevProvider(t *testing.T) {
	reg, err := buildGatewayRegistry(
		true,
		appconfig.Alipay{},
		appconfig.PayPal{},
		appconfig.WeChat{},
	)
	if err != nil {
		t.Fatalf("buildGatewayRegistry: %v", err)
	}
	provider, ok := reg.Get("dev")
	if !ok {
		t.Fatal("explicit dev gateway was not registered")
	}
	if provider.Name() != "dev" {
		t.Fatalf("provider name = %q", provider.Name())
	}
	if _, exists := reg["alipay"]; exists {
		t.Fatal("dev mode registered a fake real-provider adapter")
	}
}

func TestBuildGatewayRegistryRegistersConfiguredPayPal(t *testing.T) {
	reg, err := buildGatewayRegistry(
		false,
		appconfig.Alipay{},
		appconfig.PayPal{ClientID: "client", ClientSecret: "secret", Sandbox: true},
		appconfig.WeChat{},
	)
	if err != nil {
		t.Fatalf("buildGatewayRegistry: %v", err)
	}
	if _, ok := reg["paypal"]; !ok {
		t.Fatal("paypal gateway was not registered")
	}
}

func TestBuildGatewayRegistryReturnsPaykitRegistry(t *testing.T) {
	reg, err := buildGatewayRegistry(
		false,
		appconfig.Alipay{},
		appconfig.PayPal{},
		appconfig.WeChat{},
	)
	if err != nil {
		t.Fatalf("buildGatewayRegistry: %v", err)
	}
	var _ paykit.Registry = reg
}

func TestBuildGatewayRegistryUsesPaykitProviderPackages(t *testing.T) {
	reg, err := buildGatewayRegistry(
		false,
		appconfig.Alipay{},
		appconfig.PayPal{ClientID: "client", ClientSecret: "secret", Sandbox: true},
		appconfig.WeChat{},
	)
	if err != nil {
		t.Fatalf("buildGatewayRegistry: %v", err)
	}
	provider, ok := reg.Get("paypal")
	if !ok {
		t.Fatal("paypal gateway was not registered")
	}
	typ := reflect.TypeOf(provider)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if got, want := typ.PkgPath(), "platform/paykit/providers/paypal"; got != want {
		t.Fatalf("paypal provider package = %q, want %q", got, want)
	}
}
