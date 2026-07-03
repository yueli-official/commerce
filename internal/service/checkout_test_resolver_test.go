package service_test

import (
	"context"
	"fmt"
	"strings"

	"platform/services/commerce/internal/service"
)

type serviceTestCheckoutResolver struct{}

func (serviceTestCheckoutResolver) CurrentCheckoutItem(_ context.Context, in service.CurrentCheckoutItemInput) (service.CurrentCheckoutItemResult, error) {
	result := service.CurrentCheckoutItemResult{
		SiteKey:      defaultTestString(in.SiteKey, "shop"),
		ExternalID:   in.ExternalID,
		VariantID:    in.VariantID,
		Title:        "Test Pack",
		VariantTitle: "Standard",
		SKU:          "TEST-STD",
		PriceCents:   1200,
		Currency:     "CNY",
		DeliveryKind: "asset_file",
		DeliveryRef:  "asset-" + strings.TrimPrefix(in.ExternalID, "variant-"),
	}
	key := in.ExternalID + " " + in.VariantID
	switch {
	case strings.Contains(key, "guest"):
		result.Title, result.VariantTitle, result.SKU = "Icon Pack", "Personal License", "ICON-PERSONAL"
		result.PriceCents, result.DeliveryRef = 1900, "asset-123"
	case strings.Contains(key, "reuse"):
		result.Title, result.SKU = "Reusable Pack", "REUSE-STD"
		result.PriceCents, result.DeliveryRef = 1900, "asset-reuse"
	case strings.Contains(key, "cancel"):
		result.Title, result.SKU = "Cancel Pack", "CANCEL-STD"
		result.PriceCents, result.DeliveryRef = 990, "asset-cancel"
	case strings.Contains(key, "disabled"):
		result.Title, result.SKU = "Disabled Pack", "DISABLED-STD"
		result.PriceCents, result.DeliveryRef = 990, "asset-disabled"
	case strings.Contains(key, "user"):
		result.Title, result.VariantTitle, result.SKU = "Template Pack", "Commercial License", "TPL-COMM"
		result.PriceCents, result.DeliveryRef = 3900, "asset-456"
	case strings.Contains(key, "points"):
		result.Title, result.VariantTitle, result.SKU = "Points Template", "Points License", "PTS-TPL"
		result.PriceCents, result.PointsCost, result.Currency = 0, 30, "POINTS"
		result.DeliveryRef = "asset-points"
	case strings.Contains(key, "library"):
		result.Title, result.SKU = "Library Pack", "LIB-STD"
		result.PriceCents, result.DeliveryRef = 1200, "asset-library"
	case strings.Contains(key, "bundle"):
		result.Title, result.SKU = "Bundle Pack", "BUNDLE-STD"
		result.PriceCents, result.DeliveryKind = 1200, "bundle"
		result.DeliveryRef = `{"items":[{"kind":"asset_file","assetId":"asset-a","enabled":true},{"kind":"asset_file","assetId":"asset-b","enabled":true}]}`
	case strings.Contains(key, "download-retry"):
		result.Title, result.SKU = "Retry Pack", "RETRY-STD"
		result.PriceCents, result.DeliveryKind = 1200, "bundle"
		result.DeliveryRef = `{"access":{"maxDownloads":1},"items":[{"kind":"asset_file","assetId":"asset-retry","enabled":true}]}`
	case strings.Contains(key, "support"):
		result.Title, result.SKU = "Support Pack", "SUP-STD"
		result.PriceCents, result.DeliveryRef = 2500, "asset-support"
	case strings.Contains(key, "audit"):
		result.Title, result.SKU = "Audit Pack", "AUDIT-STD"
		result.PriceCents, result.Currency, result.DeliveryRef = 4200, "USD", "asset-audit"
	case strings.Contains(key, "free"):
		result.Title, result.VariantTitle, result.SKU = "Community Pack", "Community", "COMM-FREE"
		result.PriceCents, result.PointsCost, result.DeliveryRef = 0, 0, "asset-free"
	}
	if result.ExternalID == "" || result.VariantID == "" {
		return service.CurrentCheckoutItemResult{}, fmt.Errorf("missing checkout identity")
	}
	return result, nil
}

func defaultTestString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
