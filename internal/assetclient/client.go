// Package assetclient signs commerce delivery refs through the shared asset
// service. It is optional: commerce can still validate its own delivery token
// without asset-service credentials, but configured deployments get a real
// download URL instead of just the asset id.
package assetclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	foundationhttpclient "github.com/yueli-official/foundation/go/httpclient"
	"platform/gokit/observability"
)

type Config struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
	HTTPClient   *http.Client
}

type Client struct {
	base         string
	tokenURL     string
	clientID     string
	clientSecret string
	scope        string
	http         *http.Client
}

type DeliveryInput struct {
	AssetID   string
	SubjectID string
	ExpiresIn int
	Reason    string
}

type DeliveryOutput struct {
	GrantID   string
	URL       string
	ExpiresAt time.Time
}

func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	tokenURL := strings.TrimSpace(cfg.TokenURL)
	if base == "" || tokenURL == "" || strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("asset delivery client requires base_url, token_url, client_id, and client_secret")
	}
	cli := cfg.HTTPClient
	if cli == nil {
		cli = http.DefaultClient
	}
	cli = observability.HTTPClient(cli)
	scope := strings.TrimSpace(cfg.Scope)
	if scope == "" {
		scope = "asset:sign"
	}
	return &Client{
		base: base, tokenURL: tokenURL, clientID: strings.TrimSpace(cfg.ClientID),
		clientSecret: cfg.ClientSecret, scope: scope, http: cli,
	}, nil
}

func (c *Client) CreateDelivery(ctx context.Context, in DeliveryInput) (DeliveryOutput, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return DeliveryOutput{}, err
	}
	body := map[string]any{
		"assetId":   strings.TrimSpace(in.AssetID),
		"subjectId": strings.TrimSpace(in.SubjectID),
		"expiresIn": in.ExpiresIn,
		"reason":    strings.TrimSpace(in.Reason),
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/delivery-grants", bytes.NewReader(b))
	if err != nil {
		return DeliveryOutput{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return DeliveryOutput{}, fmt.Errorf("asset delivery grant unreachable: %w", err)
	}
	defer resp.Body.Close()
	out, err := foundationhttpclient.DecodeJSON[struct {
		GrantID   string `json:"grantId"`
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	}](resp, foundationhttpclient.Limits{})
	if err != nil {
		return DeliveryOutput{}, fmt.Errorf("asset delivery grant failed: %w", err)
	}
	if strings.TrimSpace(out.GrantID) == "" || strings.TrimSpace(out.URL) == "" {
		return DeliveryOutput{}, fmt.Errorf("asset delivery grant returned incomplete result")
	}
	var exp time.Time
	if out.ExpiresAt != "" {
		exp, err = time.Parse(time.RFC3339, out.ExpiresAt)
		if err != nil {
			return DeliveryOutput{}, fmt.Errorf("asset delivery expiry parse: %w", err)
		}
	}
	return DeliveryOutput{GrantID: out.GrantID, URL: out.URL, ExpiresAt: exp}, nil
}

func (c *Client) RevokeDelivery(ctx context.Context, grantID string) error {
	grantID = strings.TrimSpace(grantID)
	if grantID == "" {
		return fmt.Errorf("asset delivery grant id is required")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.base+"/api/v1/admin/assets/grants/"+url.PathEscape(grantID)+"/revoke",
		nil,
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("asset delivery grant revoke unreachable: %w", err)
	}
	defer resp.Body.Close()
	if _, err := foundationhttpclient.DecodeJSON[struct {
		Revoked bool `json:"revoked"`
	}](resp, foundationhttpclient.Limits{}); err != nil {
		return fmt.Errorf("asset delivery grant revoke failed: %w", err)
	}
	return nil
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("scope", c.scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("asset token unreachable: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("asset token decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || strings.TrimSpace(out.AccessToken) == "" {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return "", fmt.Errorf("asset token failed: %s", out.Error)
	}
	return out.AccessToken, nil
}
