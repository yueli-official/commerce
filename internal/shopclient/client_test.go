package shopclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVariantDeliveryKeepsAccessRulesInBundleRef(t *testing.T) {
	out, err := variantDelivery(variantView{
		DeliveryKind: "bundle",
		DeliveryPayload: deliveryPayload{
			UpdatePolicy: "latest",
			Access: deliveryAccess{
				ExpiresDays:        30,
				MaxDownloads:       1,
				DownloadLinkTTLMin: 20,
			},
		},
		DeliveryItems: []deliveryItemView{{
			Kind:    "asset_file",
			AssetID: "asset-1",
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("variantDelivery: %v", err)
	}
	if out.DeliveryKind != "bundle" {
		t.Fatalf("kind = %q, want bundle", out.DeliveryKind)
	}
	var payload deliveryPayload
	if err := json.Unmarshal([]byte(out.DeliveryRef), &payload); err != nil {
		t.Fatalf("decode delivery ref: %v", err)
	}
	if payload.Access.ExpiresDays != 30 || payload.Access.MaxDownloads != 1 || payload.Access.DownloadLinkTTLMin != 20 {
		t.Fatalf("access = %+v, want configured rules", payload.Access)
	}
	if len(payload.Items) != 1 || payload.Items[0].AssetID != "asset-1" {
		t.Fatalf("items = %+v, want asset-1", payload.Items)
	}
}

func TestCurrentCheckoutItemUsesAuthoritativeShopProductSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v1/shop/products/by-id/product-1") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code":"ok",
			"data":{
				"product":{
					"id":"product-1",
					"title":"Design Pack",
					"status":"active",
					"variants":[{
						"id":"variant-1",
						"sku":"DESIGN-STD",
						"title":"Standard",
						"priceCents":4900,
						"currency":"CNY",
						"pointsCost":30,
						"purchaseLimitPerBuyer":1,
						"status":"active",
						"deliveryKind":"bundle",
						"deliveryPayload":{"updatePolicy":"latest","access":{"maxDownloads":1}},
						"deliveryItems":[{"kind":"asset_file","assetId":"asset-real","enabled":true,"required":true}]
					}]
				}
			}
		}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := client.CurrentCheckoutItem(context.Background(), CurrentDeliveryInput{
		SiteKey: "shop", ExternalID: "product-1", VariantID: "variant-1",
	})
	if err != nil {
		t.Fatalf("CurrentCheckoutItem: %v", err)
	}
	if out.PriceCents != 4900 || out.SKU != "DESIGN-STD" || out.PurchaseLimitPerBuyer != 1 {
		t.Fatalf("checkout snapshot = %+v, want authoritative price/sku/limit", out)
	}
	if !strings.Contains(out.DeliveryRef, `"maxDownloads":1`) || !strings.Contains(out.DeliveryRef, "asset-real") {
		t.Fatalf("delivery ref = %s, want access and asset", out.DeliveryRef)
	}
}

func TestCurrentCheckoutItemRejectsInactiveProduct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code":"ok",
			"data":{"product":{"id":"product-1","title":"Draft Pack","status":"draft","variants":[]}}
		}`))
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.CurrentCheckoutItem(context.Background(), CurrentDeliveryInput{
		SiteKey: "shop", ExternalID: "product-1", VariantID: "variant-1",
	})
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("CurrentCheckoutItem inactive product error = %v, want not active", err)
	}
}
