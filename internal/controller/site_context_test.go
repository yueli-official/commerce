package controller

import (
	"context"
	"testing"

	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/sitecontext"
)

func TestCheckoutItemsForceTrustedSiteForEveryItem(t *testing.T) {
	resolver := sitecontext.New([]sitecontext.Context{{SiteKey: "shop-ae"}})
	ctx := sitecontext.With(context.Background(), sitecontext.Context{SiteKey: "shop-ae"})

	items, err := checkoutItems(ctx, resolver, []v1.CheckoutItemReq{
		{SiteKey: "shop-ui", ExternalID: "product-1"},
		{SiteKey: "", ExternalID: "product-2"},
	})
	if err != nil {
		t.Fatalf("checkoutItems() error = %v", err)
	}
	for i, item := range items {
		if item.SiteKey != "shop-ae" {
			t.Fatalf("items[%d].SiteKey = %q, want shop-ae", i, item.SiteKey)
		}
	}
}
