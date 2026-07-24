package paypal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"platform/paykit"
)

const (
	paypalSandboxBaseURL = "https://api-m.sandbox.paypal.com"
	paypalLiveBaseURL    = "https://api-m.paypal.com"
)

type Config struct {
	ClientID     string
	ClientSecret string
	Sandbox      bool
	BaseURL      string
	HTTPClient   *http.Client
}

type paypalProvider struct {
	clientID     string
	clientSecret string
	baseURL      string
	httpClient   *http.Client
}

func (p *paypalProvider) Name() string {
	return "paypal"
}

func (p *paypalProvider) CheckHealth(ctx context.Context) error {
	_, err := p.accessToken(ctx)
	return err
}

func NewProvider(cfg Config) (paykit.Provider, error) {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, fmt.Errorf("paypal client id is required")
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("paypal client secret is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		if cfg.Sandbox {
			baseURL = paypalSandboxBaseURL
		} else {
			baseURL = paypalLiveBaseURL
		}
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &paypalProvider{
		clientID:     strings.TrimSpace(cfg.ClientID),
		clientSecret: strings.TrimSpace(cfg.ClientSecret),
		baseURL:      baseURL,
		httpClient:   httpClient,
	}, nil
}

func (p *paypalProvider) CreatePayment(ctx context.Context, in paykit.CreatePaymentIn) (*paykit.CreatePaymentOut, error) {
	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	reqBody := paypalCreateOrderReq{
		Intent: "CAPTURE",
		PurchaseUnits: []paypalPurchaseUnit{{
			ReferenceID: in.OrderNo,
			Description: truncatePayPalDescription(in.Subject),
			Amount: paypalAmount{
				CurrencyCode: currency,
				Value:        centsToPayPalAmount(in.AmountCents),
			},
		}},
	}
	var out paypalCreateOrderRes
	if err := p.doJSON(ctx, http.MethodPost, "/v2/checkout/orders", token, reqBody, &out); err != nil {
		return nil, fmt.Errorf("paypal create order: %w", err)
	}
	if out.ID == "" {
		return nil, fmt.Errorf("paypal create order returned empty id")
	}
	return &paykit.CreatePaymentOut{
		Provider:    "paypal",
		Method:      string(paykit.CapabilityBrowserButton),
		SessionID:   out.ID,
		ClientToken: out.ID,
	}, nil
}

func (p *paypalProvider) CapturePayment(ctx context.Context, in paykit.CapturePaymentIn) (*paykit.CapturePaymentOut, error) {
	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var out paypalCaptureOrderRes
	path := "/v2/checkout/orders/" + url.PathEscape(in.SessionID) + "/capture"
	if err := p.doJSON(ctx, http.MethodPost, path, token, map[string]any{}, &out); err != nil {
		return nil, fmt.Errorf("paypal capture order: %w", err)
	}
	capture, err := out.completedCapture()
	if err != nil {
		return nil, err
	}
	amountCents, err := paypalAmountToCents(capture.Amount.Value)
	if err != nil {
		return nil, fmt.Errorf("paypal capture amount: %w", err)
	}
	if in.AmountCents > 0 && amountCents != in.AmountCents {
		return nil, fmt.Errorf("paypal capture amount mismatch: got %d, want %d", amountCents, in.AmountCents)
	}
	return &paykit.CapturePaymentOut{
		Success:      true,
		OrderNo:      in.OrderNo,
		ProviderTxID: capture.ID,
		AmountCents:  amountCents,
	}, nil
}

func (p *paypalProvider) VerifyNotify(context.Context, []byte, map[string]string) (*paykit.NotifyOut, error) {
	return nil, paykit.ErrUnsupportedOperation
}

func (p *paypalProvider) QueryPayment(ctx context.Context, in paykit.QueryPaymentIn) (*paykit.QueryPaymentOut, error) {
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("paypal order session id is required for payment query")
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var order paypalCaptureOrderRes
	if err := p.doJSON(
		ctx, http.MethodGet, "/v2/checkout/orders/"+url.PathEscape(sessionID),
		token, nil, &order,
	); err != nil {
		return nil, fmt.Errorf("paypal query order: %w", err)
	}
	return queryOutFromOrder(order, in.OrderNo, in.AmountCents, in.Currency)
}

func (p *paypalProvider) Refund(ctx context.Context, in paykit.RefundIn) (*paykit.RefundOut, error) {
	if strings.TrimSpace(in.ProviderTxID) == "" {
		return nil, fmt.Errorf("paypal capture id is required for refund")
	}
	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	reqBody := paypalRefundReq{
		Amount: paypalAmount{
			CurrencyCode: strings.ToUpper(strings.TrimSpace(in.Currency)),
			Value:        centsToPayPalAmount(in.AmountCents),
		},
		NoteToPayer: in.Reason,
	}
	var out paypalRefundRes
	path := "/v2/payments/captures/" + url.PathEscape(in.ProviderTxID) + "/refund"
	if err := p.doJSONWithHeaders(
		ctx, http.MethodPost, path, token, reqBody, &out,
		map[string]string{"PayPal-Request-Id": in.IdempotencyKey},
	); err != nil {
		return nil, fmt.Errorf("paypal refund capture: %w", err)
	}
	if out.ID == "" {
		return nil, fmt.Errorf("paypal refund returned empty id")
	}
	result := &paykit.RefundOut{
		ProviderID: out.ID, AmountCents: in.AmountCents,
		Currency:       strings.ToUpper(strings.TrimSpace(in.Currency)),
		ProviderStatus: out.Status,
	}
	switch out.Status {
	case "COMPLETED":
		result.Success = true
		result.Status = paykit.RefundStatusSucceeded
	case "PENDING", "":
		result.Status = paykit.RefundStatusPending
	case "FAILED":
		result.Status = paykit.RefundStatusFailed
	case "CANCELLED":
		result.Status = paykit.RefundStatusCancelled
	default:
		return nil, fmt.Errorf("paypal refund status %q", out.Status)
	}
	return result, nil
}

func (p *paypalProvider) accessToken(ctx context.Context) (string, error) {
	form := strings.NewReader("grant_type=client_credentials")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/oauth2/token", form)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(p.clientID, p.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("paypal oauth token: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return "", fmt.Errorf("paypal oauth token status %d: %s", res.StatusCode, string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("paypal oauth decode: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("paypal oauth returned empty access token")
	}
	return out.AccessToken, nil
}

func (p *paypalProvider) doJSON(ctx context.Context, method, path, token string, in any, out any) error {
	return p.doJSONWithHeaders(ctx, method, path, token, in, out, nil)
}

func (p *paypalProvider) doJSONWithHeaders(
	ctx context.Context,
	method, path, token string,
	in any,
	out any,
	headers map[string]string,
) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}

	res, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("status %d: %s", res.StatusCode, string(respBody))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

func centsToPayPalAmount(cents int) string {
	return fmt.Sprintf("%.2f", float64(cents)/100)
}

func paypalAmountToCents(value string) (int, error) {
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return int(math.Round(amount * 100)), nil
}

func truncatePayPalDescription(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 127 {
		return s
	}
	return s[:127]
}

type paypalCreateOrderReq struct {
	Intent        string               `json:"intent"`
	PurchaseUnits []paypalPurchaseUnit `json:"purchase_units"`
}

type paypalPurchaseUnit struct {
	ReferenceID string       `json:"reference_id"`
	Description string       `json:"description,omitempty"`
	Amount      paypalAmount `json:"amount"`
}

type paypalAmount struct {
	CurrencyCode string `json:"currency_code"`
	Value        string `json:"value"`
}

type paypalCreateOrderRes struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type paypalCaptureOrderRes struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	PurchaseUnits []struct {
		ReferenceID string       `json:"reference_id"`
		Amount      paypalAmount `json:"amount"`
		Payments    struct {
			Captures []paypalCapture `json:"captures"`
		} `json:"payments"`
	} `json:"purchase_units"`
}

func queryOutFromOrder(
	order paypalCaptureOrderRes,
	expectedOrderNo string,
	expectedAmountCents int,
	expectedCurrency string,
) (*paykit.QueryPaymentOut, error) {
	if order.ID == "" || order.Status == "" {
		return nil, fmt.Errorf("paypal query returned incomplete order")
	}
	out := &paykit.QueryPaymentOut{
		OrderNo: expectedOrderNo, ObservationID: "paypal:query:" + order.ID + ":" + order.Status,
		ProviderStatus: order.Status,
	}
	if len(order.PurchaseUnits) > 0 {
		unit := order.PurchaseUnits[0]
		if strings.TrimSpace(unit.ReferenceID) != "" {
			out.OrderNo = strings.TrimSpace(unit.ReferenceID)
		}
		if unit.Amount.Value != "" {
			amountCents, err := paypalAmountToCents(unit.Amount.Value)
			if err != nil {
				return nil, fmt.Errorf("paypal query amount: %w", err)
			}
			out.AmountCents = amountCents
			out.Currency = strings.ToUpper(strings.TrimSpace(unit.Amount.CurrencyCode))
		}
	}
	switch order.Status {
	case "COMPLETED":
		capture, err := order.completedCapture()
		if err != nil {
			return nil, err
		}
		out.ProviderTxID = capture.ID
		out.AmountCents, err = paypalAmountToCents(capture.Amount.Value)
		if err != nil {
			return nil, fmt.Errorf("paypal capture query amount: %w", err)
		}
		out.Currency = strings.ToUpper(strings.TrimSpace(capture.Amount.CurrencyCode))
		out.Success = true
		out.Status = paykit.PaymentStatusSettled
	case "CREATED", "SAVED", "APPROVED", "PAYER_ACTION_REQUIRED":
		out.Status = paykit.PaymentStatusPending
	case "VOIDED":
		out.Status = paykit.PaymentStatusCancelled
	default:
		return nil, fmt.Errorf("paypal query unsupported order status %q", order.Status)
	}
	if expectedOrderNo != "" && out.OrderNo != expectedOrderNo {
		return nil, fmt.Errorf("paypal query order mismatch: got %q, want %q", out.OrderNo, expectedOrderNo)
	}
	if expectedAmountCents > 0 && out.AmountCents != expectedAmountCents {
		return nil, fmt.Errorf(
			"paypal query amount mismatch: got %d, want %d",
			out.AmountCents, expectedAmountCents,
		)
	}
	expectedCurrency = strings.ToUpper(strings.TrimSpace(expectedCurrency))
	if expectedCurrency != "" && out.Currency != expectedCurrency {
		return nil, fmt.Errorf(
			"paypal query currency mismatch: got %q, want %q",
			out.Currency, expectedCurrency,
		)
	}
	return out, nil
}

type paypalCapture struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Amount paypalAmount `json:"amount"`
}

func (r paypalCaptureOrderRes) completedCapture() (*paypalCapture, error) {
	if r.Status != "COMPLETED" {
		return nil, fmt.Errorf("paypal order status %q", r.Status)
	}
	for _, unit := range r.PurchaseUnits {
		for _, capture := range unit.Payments.Captures {
			if capture.Status == "COMPLETED" && capture.ID != "" {
				return &capture, nil
			}
		}
	}
	return nil, fmt.Errorf("paypal completed capture not found")
}

type paypalRefundReq struct {
	Amount      paypalAmount `json:"amount"`
	NoteToPayer string       `json:"note_to_payer,omitempty"`
}

type paypalRefundRes struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
