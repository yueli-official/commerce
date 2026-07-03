package notificationclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	BaseURL    string
	APIToken   string
	HTTPClient *http.Client
}

type Client struct {
	base     string
	apiToken string
	http     *http.Client
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
	if base == "" {
		return nil, fmt.Errorf("notification client requires base_url")
	}
	cli := cfg.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{base: base, apiToken: strings.TrimSpace(cfg.APIToken), http: cli}, nil
}

func (c *Client) Send(ctx context.Context, in SendInput) (SendOutput, error) {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/notifications/send", bytes.NewReader(b))
	if err != nil {
		return SendOutput{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("X-Notification-Token", c.apiToken)
	}
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
