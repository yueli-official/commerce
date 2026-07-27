// Package shopclient resolves current shop delivery configuration for commerce.
package shopclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	foundationhttpclient "github.com/yueli-official/foundation/go/httpclient"
	commerceruntime "github.com/yueli-official/commerce/internal/runtime"
)

type Config struct {
	BaseURL    string
	HTTPClient *http.Client
}

type Client struct {
	base string
	http *http.Client
}

type CurrentDeliveryInput struct {
	SiteKey    string
	ExternalID string
	VariantID  string
}

type CurrentDeliveryOutput struct {
	DeliveryKind string
	DeliveryRef  string
}

type CurrentCheckoutItemOutput struct {
	SiteKey               string
	ExternalID            string
	VariantID             string
	Title                 string
	VariantTitle          string
	SKU                   string
	PriceCents            int
	PointsCost            int
	Currency              string
	DeliveryKind          string
	DeliveryRef           string
	PurchaseLimitPerBuyer int
}

func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("shop delivery client requires base_url")
	}
	cli := cfg.HTTPClient
	if cli == nil {
		cli = http.DefaultClient
	}
	cli = commerceruntime.TelemetryHTTPClient(cli)
	return &Client{base: base, http: cli}, nil
}

func (c *Client) CurrentDelivery(ctx context.Context, in CurrentDeliveryInput) (CurrentDeliveryOutput, error) {
	product, err := c.product(ctx, strings.TrimSpace(in.ExternalID))
	if err != nil {
		return CurrentDeliveryOutput{}, err
	}
	variant := selectVariant(product.Variants, strings.TrimSpace(in.VariantID))
	if variant == nil {
		return CurrentDeliveryOutput{}, fmt.Errorf("shop current delivery variant not found")
	}
	return variantDelivery(*variant)
}

func (c *Client) CurrentCheckoutItem(ctx context.Context, in CurrentDeliveryInput) (CurrentCheckoutItemOutput, error) {
	product, err := c.product(ctx, strings.TrimSpace(in.ExternalID))
	if err != nil {
		return CurrentCheckoutItemOutput{}, err
	}
	if product.Status != "" && product.Status != "active" {
		return CurrentCheckoutItemOutput{}, fmt.Errorf("shop checkout product is not active")
	}
	variant := selectVariant(product.Variants, strings.TrimSpace(in.VariantID))
	if variant == nil {
		return CurrentCheckoutItemOutput{}, fmt.Errorf("shop checkout variant not found")
	}
	if variant.Status != "" && variant.Status != "active" {
		return CurrentCheckoutItemOutput{}, fmt.Errorf("shop checkout variant is not active")
	}
	delivery, err := variantDelivery(*variant)
	if err != nil {
		return CurrentCheckoutItemOutput{}, err
	}
	return CurrentCheckoutItemOutput{
		SiteKey:               strings.TrimSpace(in.SiteKey),
		ExternalID:            strings.TrimSpace(product.ID),
		VariantID:             strings.TrimSpace(variant.ID),
		Title:                 strings.TrimSpace(product.Title),
		VariantTitle:          strings.TrimSpace(variant.Title),
		SKU:                   strings.TrimSpace(variant.SKU),
		PriceCents:            variant.PriceCents,
		PointsCost:            variant.PointsCost,
		Currency:              strings.TrimSpace(variant.Currency),
		DeliveryKind:          delivery.DeliveryKind,
		DeliveryRef:           delivery.DeliveryRef,
		PurchaseLimitPerBuyer: variant.PurchaseLimitPerBuyer,
	}, nil
}

func (c *Client) product(ctx context.Context, productID string) (productView, error) {
	if productID == "" {
		return productView{}, fmt.Errorf("shop current delivery requires product id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/shop/products/by-id/"+url.PathEscape(productID), nil)
	if err != nil {
		return productView{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return productView{}, fmt.Errorf("shop current delivery unreachable: %w", err)
	}
	defer resp.Body.Close()
	out, err := foundationhttpclient.DecodeJSON[struct {
		Product productView `json:"product"`
	}](resp, foundationhttpclient.Limits{})
	if err != nil {
		return productView{}, fmt.Errorf("shop current delivery failed: %w", err)
	}
	return out.Product, nil
}

func selectVariant(variants []variantView, variantID string) *variantView {
	for i := range variants {
		if variantID != "" && variants[i].ID == variantID {
			return &variants[i]
		}
	}
	if variantID != "" {
		return nil
	}
	for i := range variants {
		if variants[i].Status == "" || variants[i].Status == "active" {
			return &variants[i]
		}
	}
	return nil
}

func variantDelivery(v variantView) (CurrentDeliveryOutput, error) {
	kind := strings.TrimSpace(v.DeliveryKind)
	if kind == "" {
		kind = "asset_file"
	}
	switch kind {
	case "asset_file":
		return CurrentDeliveryOutput{DeliveryKind: kind, DeliveryRef: strings.TrimSpace(v.DeliveryAssetID)}, nil
	case "netdisk":
		raw, err := json.Marshal(map[string]any{"netdisk": v.DeliveryPayload.Netdisk})
		if err != nil {
			return CurrentDeliveryOutput{}, err
		}
		return CurrentDeliveryOutput{DeliveryKind: kind, DeliveryRef: string(raw)}, nil
	case "bundle":
		raw, err := json.Marshal(deliveryPayload{
			UpdatePolicy: v.DeliveryPayload.UpdatePolicy,
			Access:       v.DeliveryPayload.Access,
			Items:        v.DeliveryItems,
		})
		if err != nil {
			return CurrentDeliveryOutput{}, err
		}
		return CurrentDeliveryOutput{DeliveryKind: kind, DeliveryRef: string(raw)}, nil
	default:
		return CurrentDeliveryOutput{}, fmt.Errorf("unsupported shop delivery kind: %s", kind)
	}
}

type productView struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Status   string        `json:"status"`
	Variants []variantView `json:"variants"`
}

type variantView struct {
	ID                    string             `json:"id"`
	SKU                   string             `json:"sku"`
	Title                 string             `json:"title"`
	PriceCents            int                `json:"priceCents"`
	Currency              string             `json:"currency"`
	PointsCost            int                `json:"pointsCost"`
	Status                string             `json:"status"`
	DeliveryKind          string             `json:"deliveryKind"`
	DeliveryAssetID       string             `json:"deliveryAssetId"`
	DeliveryPayload       deliveryPayload    `json:"deliveryPayload"`
	DeliveryUpdatePolicy  string             `json:"deliveryUpdatePolicy"`
	DeliveryItems         []deliveryItemView `json:"deliveryItems"`
	PurchaseLimitPerBuyer int                `json:"purchaseLimitPerBuyer"`
}

type deliveryPayload struct {
	Netdisk      *netdiskDelivery   `json:"netdisk,omitempty"`
	UpdatePolicy string             `json:"updatePolicy,omitempty"`
	Access       deliveryAccess     `json:"access,omitempty"`
	Items        []deliveryItemView `json:"items,omitempty"`
}

type deliveryAccess struct {
	ExpiresDays        int `json:"expiresDays,omitempty"`
	MaxDownloads       int `json:"maxDownloads,omitempty"`
	DownloadLinkTTLMin int `json:"downloadLinkTTLMin,omitempty"`
}

type deliveryItemView struct {
	ID       string           `json:"id,omitempty"`
	Kind     string           `json:"kind"`
	Title    string           `json:"title,omitempty"`
	AssetID  string           `json:"assetId,omitempty"`
	Netdisk  *netdiskDelivery `json:"netdisk,omitempty"`
	Sort     int              `json:"sort,omitempty"`
	Enabled  bool             `json:"enabled"`
	Required bool             `json:"required"`
}

type netdiskDelivery struct {
	Provider    string `json:"provider,omitempty"`
	URL         string `json:"url,omitempty"`
	AccessCode  string `json:"accessCode,omitempty"`
	ExtractCode string `json:"extractCode,omitempty"`
	Note        string `json:"note,omitempty"`
}
