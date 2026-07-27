package service_test

import (
	"context"
	"testing"

	"github.com/yueli-official/commerce/internal/commerceerr"
	"github.com/yueli-official/commerce/internal/service"
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
	value, ok := commerceerr.Resolve(err)
	if !ok || value.Code != commerceerr.CodeInvalidRequest {
		t.Fatalf("CreateCheckout error = %v, want %s", err, commerceerr.CodeInvalidRequest)
	}
}
