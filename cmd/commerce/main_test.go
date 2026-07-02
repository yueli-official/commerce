package main

import (
	"reflect"
	"testing"

	"platform/paykit"
	"platform/services/commerce/internal/appconfig"
)

func TestBuildGatewayRegistryDoesNotRegisterStubsForDevSettle(t *testing.T) {
	reg, err := buildGatewayRegistry(
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

func TestBuildGatewayRegistryRegistersConfiguredPayPal(t *testing.T) {
	reg, err := buildGatewayRegistry(
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
