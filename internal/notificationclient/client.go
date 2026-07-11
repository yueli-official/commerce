package notificationclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

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

type Recipient struct {
	UserSub string `json:"userSub,omitempty"`
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
}

type SendInput struct {
	IdempotencyKey string            `json:"idempotencyKey"`
	Scene          string            `json:"scene"`
	Channel        string            `json:"channel"`
	Recipient      Recipient         `json:"recipient"`
	Locale         string            `json:"locale,omitempty"`
	Data           map[string]string `json:"data,omitempty"`
}

type SendOutput struct {
	MessageID string `json:"messageId"`
	Status    string `json:"status"`
	Provider  string `json:"provider"`
	Duplicate bool   `json:"duplicate"`
}

func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" || strings.TrimSpace(cfg.TokenURL) == "" || strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("notification client requires base_url, token_url, client_id, and client_secret")
	}
	cli := cfg.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 5 * time.Second}
	}
	cli = observability.HTTPClient(cli)
	scope := strings.TrimSpace(cfg.Scope)
	if scope == "" {
		scope = "notification:send"
	}
	return &Client{base: base, tokenURL: strings.TrimSpace(cfg.TokenURL), clientID: strings.TrimSpace(cfg.ClientID), clientSecret: cfg.ClientSecret, scope: scope, http: cli}, nil
}

func (c *Client) Send(ctx context.Context, in SendInput) (SendOutput, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return SendOutput{}, err
	}
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/notifications/send", bytes.NewReader(b))
	if err != nil {
		return SendOutput{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return SendOutput{}, fmt.Errorf("notification service unreachable: %w", err)
	}
	defer resp.Body.Close()
	var env struct {
		Code    string     `json:"code"`
		Message string     `json:"message"`
		Data    SendOutput `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return SendOutput{}, fmt.Errorf("notification response decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || env.Code != "ok" {
		if env.Message == "" {
			env.Message = env.Code
		}
		return SendOutput{}, fmt.Errorf("notification send failed: %s", env.Message)
	}
	return env.Data, nil
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.clientID}, "client_secret": {c.clientSecret}, "scope": {c.scope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("notification token unreachable: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("notification token decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || strings.TrimSpace(out.AccessToken) == "" {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return "", fmt.Errorf("notification token failed: %s", out.Error)
	}
	return out.AccessToken, nil
}
