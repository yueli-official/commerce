package service_test

import (
	"context"
	"strings"
	"testing"

	"platform/services/commerce/internal/service"
)

func TestCreateCheckoutRequiresCurrentCatalogResolver(t *testing.T) {
	svc := service.New(nil, service.CheckinConfig{})

	_, err := svc.CreateCheckout(context.Background(), service.CheckoutDesc{
		BuyerEmail: "buyer@example.com",
		Provider:   "alipay",
		Items: []service.CheckoutItemDesc{{
			SiteKey:    "shop",
			ExternalID: "product-1",
			VariantID:  "variant-1",
			Title:      "Tampered Product",
			PriceCents: 1,
			Currency:   "CNY",
			Quantity:   1,
		}},
	})
	if err == nil {
		t.Fatal("CreateCheckout without current catalog resolver returned nil error")
	}
	if !strings.Contains(err.Error(), "checkout catalog resolver is required") {
		t.Fatalf("CreateCheckout error = %v, want checkout catalog resolver is required", err)
	}
}
