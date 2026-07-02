package main

import (
	"testing"

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
