package gateway

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/smartwalle/alipay/v3"
)

// AlipayConfig holds the configuration fields read from the commerce.alipay
// config block (manifest/config/config.yaml + GF_* env overrides).
type AlipayConfig struct {
	AppID           string
	PrivateKey      string
	AlipayPublicKey string
	Sandbox         bool
}

// alipayProvider implements PaymentGateway using the Alipay page-pay flow.
type alipayProvider struct {
	client *alipay.Client
}

// NewAlipayProvider constructs an alipayProvider from the given config.
// It mirrors the donor's newAlipayClient logic: normalises key strings,
// constructs the client, and loads the Alipay public key for signature
// verification.
func NewAlipayProvider(cfg AlipayConfig) (PaymentGateway, error) {
	privateKey := normalizeAlipayKey(cfg.PrivateKey)
	publicKey := normalizeAlipayKey(cfg.AlipayPublicKey)

	client, err := alipay.New(cfg.AppID, privateKey, !cfg.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("init alipay client: %w", err)
	}
	if err := client.LoadAliPayPublicKey(publicKey); err != nil {
		return nil, fmt.Errorf("load alipay public key: %w", err)
	}
	return &alipayProvider{client: client}, nil
}

// normalizeAlipayKey strips PEM headers, whitespace, and literal \n sequences
// from a key string, yielding bare base64 as the alipay library expects.
// Ported verbatim from the donor plugin's normalizeKey function.
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

// CreatePayment creates an Alipay page-pay session and returns the redirect URL.
// The amount is converted from cents to a decimal string (e.g. 9900 → "99.00").
func (p *alipayProvider) CreatePayment(_ context.Context, in CreateIn) (string, error) {
	trade := alipay.TradePagePay{
		Trade: alipay.Trade{
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
		return "", fmt.Errorf("alipay TradePagePay: %w", err)
	}
	return payURL.String(), nil
}

// VerifyNotify authenticates an Alipay async notify callback.
// It parses the form-encoded body, verifies the RSA2 signature via
// DecodeNotification, then maps trade status to NotifyOut.
// Returns an error for any signature failure or body parse error.
func (p *alipayProvider) VerifyNotify(_ context.Context, body []byte, _ map[string]string) (*NotifyOut, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("alipay notify parse: %w", err)
	}

	// DecodeNotification verifies the RSA2 signature internally via VerifySign.
	notify, err := p.client.DecodeNotification(values)
	if err != nil {
		return nil, fmt.Errorf("alipay notify verify: %w", err)
	}

	status := notify.TradeStatus
	if status == alipay.TradeStatusSuccess || status == alipay.TradeStatusFinished {
		amount, err := strconv.ParseFloat(notify.TotalAmount, 64)
		if err != nil {
			return nil, fmt.Errorf("alipay notify amount parse %q: %w", notify.TotalAmount, err)
		}
		return &NotifyOut{
			Success:      true,
			OrderNo:      notify.OutTradeNo,
			ProviderTxID: notify.TradeNo,
			AmountCents:  int(math.Round(amount * 100)),
		}, nil
	}
	return &NotifyOut{Success: false}, nil
}
