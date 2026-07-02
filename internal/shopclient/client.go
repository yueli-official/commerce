// Package shopclient resolves current shop delivery configuration for commerce.
package shopclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("shop delivery client requires base_url")
	}
	cli := cfg.HTTPClient
	if cli == nil {
		cli = http.DefaultClient
	}
	return &Client{base: base, http: cli}, nil
}

func (c *Client) CurrentDelivery(ctx context.Context, in CurrentDeliveryInput) (CurrentDeliveryOutput, error) {
	productID := strings.TrimSpace(in.ExternalID)
	if productID == "" {
		return CurrentDeliveryOutput{}, fmt.Errorf("shop current delivery requires product id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/shop/products/by-id/"+url.PathEscape(productID), nil)
	if err != nil {
		return CurrentDeliveryOutput{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return CurrentDeliveryOutput{}, fmt.Errorf("shop current delivery unreachable: %w", err)
	}
	defer resp.Body.Close()
	var env productEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return CurrentDeliveryOutput{}, fmt.Errorf("shop current delivery decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || env.Code != "ok" {
		if env.Message == "" {
			env.Message = env.Code
		}
		return CurrentDeliveryOutput{}, fmt.Errorf("shop current delivery failed: %s", env.Message)
	}
	variant := selectVariant(env.Data.Product.Variants, strings.TrimSpace(in.VariantID))
	if variant == nil {
		return CurrentDeliveryOutput{}, fmt.Errorf("shop current delivery variant not found")
	}
	return variantDelivery(*variant)
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

type productEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Product productView `json:"product"`
	} `json:"data"`
}

type productView struct {
	ID       string        `json:"id"`
	Variants []variantView `json:"variants"`
}

type variantView struct {
	ID                   string             `json:"id"`
	Status               string             `json:"status"`
	DeliveryKind         string             `json:"deliveryKind"`
	DeliveryAssetID      string             `json:"deliveryAssetId"`
	DeliveryPayload      deliveryPayload    `json:"deliveryPayload"`
	DeliveryUpdatePolicy string             `json:"deliveryUpdatePolicy"`
	DeliveryItems        []deliveryItemView `json:"deliveryItems"`
}

type deliveryPayload struct {
	Netdisk      *netdiskDelivery   `json:"netdisk,omitempty"`
	UpdatePolicy string             `json:"updatePolicy,omitempty"`
	Items        []deliveryItemView `json:"items,omitempty"`
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
