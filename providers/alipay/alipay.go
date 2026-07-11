package alipay

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	smartalipay "github.com/smartwalle/alipay/v3"

	"platform/paykit"
)

// AlipayConfig holds the configuration fields read from the commerce.alipay
// config block (manifest/config/config.yaml + GF_* env overrides).
type Config struct {
	AppID           string
	PrivateKey      string
	AlipayPublicKey string
	Sandbox         bool
	HTTPClient      *http.Client
}

// alipayProvider implements PaymentGateway using the Alipay page-pay flow.
type alipayProvider struct {
	client *smartalipay.Client
	appID  string // configured merchant app_id; verified against every notify
}

func (p *alipayProvider) Name() string {
	return "alipay"
}

// NewProvider constructs an alipayProvider from the given config, loading
// the Alipay public key needed to verify async-notify signatures.
func NewProvider(cfg Config) (paykit.Provider, error) {
	privateKey := normalizeAlipayKey(cfg.PrivateKey)
	publicKey := normalizeAlipayKey(cfg.AlipayPublicKey)

	client, err := smartalipay.New(cfg.AppID, privateKey, !cfg.Sandbox, smartalipay.WithHTTPClient(cfg.HTTPClient))
	if err != nil {
		return nil, fmt.Errorf("init alipay client: %w", err)
	}
	if err := client.LoadAliPayPublicKey(publicKey); err != nil {
		return nil, fmt.Errorf("load alipay public key: %w", err)
	}
	return &alipayProvider{client: client, appID: cfg.AppID}, nil
}

// normalizeAlipayKey strips PEM headers, whitespace, and literal \n sequences
// from a key string, yielding bare base64 as the alipay library expects.
func normalizeAlipayKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return key
	}
	key = strings.ReplaceAll(key, `\n`, "\n")
	if strings.HasPrefix(key, "-----") {
		lines := strings.Split(key, "\n")
		var b64 []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "-----") {
				continue
			}
			b64 = append(b64, line)
		}
		return strings.Join(b64, "")
	}
	key = strings.ReplaceAll(key, "\n", "")
	key = strings.ReplaceAll(key, "\r", "")
	key = strings.ReplaceAll(key, " ", "")
	return key
}

// CheckNotifyAppID returns an error when the app_id carried in an Alipay
// async-notify does not match the merchant's configured app_id.
// Exported so it can be unit-tested in isolation without a fully-signed payload.
func CheckNotifyAppID(notifyAppID, configuredAppID string) error {
	if notifyAppID != configuredAppID {
		return fmt.Errorf("alipay notify app_id mismatch: got %q, want %q", notifyAppID, configuredAppID)
	}
	return nil
}

// CreatePayment creates an Alipay page-pay session and returns the redirect URL.
// The amount is converted from cents to a decimal string (e.g. 9900 → "99.00").
func (p *alipayProvider) CreatePayment(_ context.Context, in paykit.CreatePaymentIn) (*paykit.CreatePaymentOut, error) {
	trade := smartalipay.TradePagePay{
		Trade: smartalipay.Trade{
			Subject:     in.Subject,
			OutTradeNo:  in.OrderNo,
			TotalAmount: fmt.Sprintf("%.2f", float64(in.AmountCents)/100),
			ProductCode: "FAST_INSTANT_TRADE_PAY",
			NotifyURL:   in.NotifyURL,
			ReturnURL:   in.ReturnURL,
		},
	}
	payURL, err := p.client.TradePagePay(trade)
	if err != nil {
		return nil, fmt.Errorf("alipay TradePagePay: %w", err)
	}
	return &paykit.CreatePaymentOut{Provider: "alipay", Method: string(paykit.CapabilityRedirect), PayURL: payURL.String()}, nil
}

func (p *alipayProvider) CapturePayment(context.Context, paykit.CapturePaymentIn) (*paykit.CapturePaymentOut, error) {
	return nil, paykit.ErrUnsupportedOperation
}

func (p *alipayProvider) QueryPayment(ctx context.Context, in paykit.QueryPaymentIn) (*paykit.QueryPaymentOut, error) {
	query := smartalipay.TradeQuery{OutTradeNo: in.OrderNo, TradeNo: in.ProviderTxID}
	rsp, err := p.client.TradeQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("alipay TradeQuery: %w", err)
	}
	if rsp == nil {
		return nil, fmt.Errorf("alipay TradeQuery returned empty response")
	}
	if rsp.IsFailure() {
		return nil, fmt.Errorf("alipay TradeQuery failed: %s %s", rsp.Code, rsp.SubMsg)
	}
	return queryOutFromTradeQuery(rsp, in.AmountCents)
}

// VerifyNotify authenticates an Alipay async notify callback.
// It parses the form-encoded body, verifies the RSA2 signature via
// DecodeNotification, then maps trade status to NotifyOut.
// Returns an error for any signature failure or body parse error.
func (p *alipayProvider) VerifyNotify(_ context.Context, body []byte, _ map[string]string) (*paykit.NotifyOut, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("alipay notify parse: %w", err)
	}

	// DecodeNotification verifies the RSA2 signature internally via VerifySign.
	notify, err := p.client.DecodeNotification(values)
	if err != nil {
		return nil, fmt.Errorf("alipay notify verify: %w", err)
	}

	// Defense-in-depth: ensure the notify targets our merchant app, not a
	// different Alipay tenant whose signature happens to verify with our key.
	if err := CheckNotifyAppID(notify.AppId, p.appID); err != nil {
		return nil, err
	}

	status := notify.TradeStatus
	if status == smartalipay.TradeStatusSuccess || status == smartalipay.TradeStatusFinished {
		amount, err := strconv.ParseFloat(notify.TotalAmount, 64)
		if err != nil {
			return nil, fmt.Errorf("alipay notify amount parse %q: %w", notify.TotalAmount, err)
		}
		return &paykit.NotifyOut{
			Success:      true,
			OrderNo:      notify.OutTradeNo,
			ProviderTxID: notify.TradeNo,
			AmountCents:  int(math.Round(amount * 100)),
		}, nil
	}
	return &paykit.NotifyOut{Success: false}, nil
}

func queryOutFromTradeQuery(rsp *smartalipay.TradeQueryRsp, expectedAmountCents int) (*paykit.QueryPaymentOut, error) {
	if rsp == nil {
		return nil, fmt.Errorf("alipay query response is empty")
	}
	status := rsp.TradeStatus
	if status != smartalipay.TradeStatusSuccess && status != smartalipay.TradeStatusFinished {
		return &paykit.QueryPaymentOut{Success: false, OrderNo: rsp.OutTradeNo, ProviderTxID: rsp.TradeNo}, nil
	}
	amount, err := strconv.ParseFloat(rsp.TotalAmount, 64)
	if err != nil {
		return nil, fmt.Errorf("alipay query amount parse %q: %w", rsp.TotalAmount, err)
	}
	amountCents := int(math.Round(amount * 100))
	if expectedAmountCents > 0 && amountCents != expectedAmountCents {
		return nil, fmt.Errorf("alipay query amount mismatch: got %d, want %d", amountCents, expectedAmountCents)
	}
	return &paykit.QueryPaymentOut{
		Success:      true,
		OrderNo:      rsp.OutTradeNo,
		ProviderTxID: rsp.TradeNo,
		AmountCents:  amountCents,
	}, nil
}

func (p *alipayProvider) Refund(context.Context, paykit.RefundIn) (*paykit.RefundOut, error) {
	return nil, paykit.ErrUnsupportedOperation
}
