package wechat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	wechatCore "github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/signers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/validators"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/downloader"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"

	"platform/paykit"
)

type Config struct {
	MerchantID       string
	MerchantCertSN   string
	MerchantAPIv3Key string
	PrivateKey       string
	AppID            string
	NotifyURL        string
	HTTPClient       *http.Client
}

type wechatProvider struct {
	appID      string
	merchantID string
	notifyURL  string
	nativeSvc  wechatNativeService
	refundSvc  wechatRefundService
	notifySvc  wechatNotifyService
}

func (p *wechatProvider) Name() string {
	return "wechat"
}

type wechatNativeService interface {
	Prepay(context.Context, native.PrepayRequest) (*native.PrepayResponse, error)
}

type wechatRefundService interface {
	Create(context.Context, refunddomestic.CreateRequest) (*refunddomestic.Refund, error)
}

type wechatNotifyService interface {
	Verify(context.Context, []byte, map[string]string) (*payments.Transaction, error)
}

func NewProvider(cfg Config) (paykit.Provider, error) {
	if strings.TrimSpace(cfg.MerchantID) == "" {
		return nil, fmt.Errorf("wechat merchant id is required")
	}
	if strings.TrimSpace(cfg.MerchantCertSN) == "" {
		return nil, fmt.Errorf("wechat merchant certificate serial number is required")
	}
	if strings.TrimSpace(cfg.MerchantAPIv3Key) == "" {
		return nil, fmt.Errorf("wechat api v3 key is required")
	}
	if strings.TrimSpace(cfg.PrivateKey) == "" {
		return nil, fmt.Errorf("wechat merchant private key is required")
	}
	if strings.TrimSpace(cfg.AppID) == "" {
		return nil, fmt.Errorf("wechat app id is required")
	}

	privateKey, err := utils.LoadPrivateKey(normalizeWeChatPEM(cfg.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("load wechat merchant private key: %w", err)
	}
	ctx := context.Background()
	mgr := downloader.MgrInstance()
	if !mgr.HasDownloader(ctx, cfg.MerchantID) {
		downloaderClient, err := wechatCore.NewClientWithDialSettings(ctx, &wechatCore.DialSettings{
			HTTPClient: cfg.HTTPClient,
			Signer: &signers.SHA256WithRSASigner{
				MchID: cfg.MerchantID, CertificateSerialNo: cfg.MerchantCertSN, PrivateKey: privateKey,
			},
			Validator: &validators.NullValidator{},
		})
		if err != nil {
			return nil, fmt.Errorf("init wechat certificate client: %w", err)
		}
		if err := mgr.RegisterDownloaderWithClient(ctx, downloaderClient, cfg.MerchantID, cfg.MerchantAPIv3Key); err != nil {
			return nil, fmt.Errorf("register wechat certificate downloader: %w", err)
		}
	}
	client, err := wechatCore.NewClient(ctx,
		option.WithWechatPayAutoAuthCipherUsingDownloaderMgr(cfg.MerchantID, cfg.MerchantCertSN, privateKey, mgr),
		option.WithHTTPClient(cfg.HTTPClient),
	)
	if err != nil {
		return nil, fmt.Errorf("init wechat pay client: %w", err)
	}
	notifyHandler, err := notify.NewRSANotifyHandler(
		cfg.MerchantAPIv3Key,
		verifiers.NewSHA256WithRSAVerifier(mgr.GetCertificateVisitor(cfg.MerchantID)),
	)
	if err != nil {
		return nil, fmt.Errorf("init wechat notify handler: %w", err)
	}

	return &wechatProvider{
		appID:      strings.TrimSpace(cfg.AppID),
		merchantID: strings.TrimSpace(cfg.MerchantID),
		notifyURL:  strings.TrimSpace(cfg.NotifyURL),
		nativeSvc:  &wechatNativeAPI{svc: &native.NativeApiService{Client: client}},
		refundSvc:  &wechatRefundAPI{svc: &refunddomestic.RefundsApiService{Client: client}},
		notifySvc:  &wechatNotifyVerifier{handler: notifyHandler},
	}, nil
}

func (p *wechatProvider) CreatePayment(ctx context.Context, in paykit.CreatePaymentIn) (*paykit.CreatePaymentOut, error) {
	if p.nativeSvc == nil {
		return nil, fmt.Errorf("wechat native service is not configured")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "CNY"
	}
	if currency != "CNY" {
		return nil, fmt.Errorf("wechat native pay supports CNY only, got %q", currency)
	}
	notifyURL := strings.TrimSpace(in.NotifyURL)
	if notifyURL == "" {
		notifyURL = p.notifyURL
	}
	if notifyURL == "" {
		return nil, fmt.Errorf("wechat notify url is required")
	}
	req := native.PrepayRequest{
		Appid:       wechatCore.String(p.appID),
		Mchid:       wechatCore.String(p.merchantID),
		Description: wechatCore.String(truncateDescription(in.Subject)),
		OutTradeNo:  wechatCore.String(in.OrderNo),
		NotifyUrl:   wechatCore.String(notifyURL),
		Amount: &native.Amount{
			Total:    wechatCore.Int64(int64(in.AmountCents)),
			Currency: wechatCore.String(currency),
		},
	}
	resp, err := p.nativeSvc.Prepay(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("wechat native prepay: %w", err)
	}
	if resp == nil || resp.CodeUrl == nil || strings.TrimSpace(*resp.CodeUrl) == "" {
		return nil, fmt.Errorf("wechat native prepay returned empty code_url")
	}
	return &paykit.CreatePaymentOut{
		Provider:  "wechat",
		Method:    string(paykit.CapabilityNativeQR),
		SessionID: in.OrderNo,
		QRCode:    *resp.CodeUrl,
	}, nil
}

func (p *wechatProvider) CapturePayment(context.Context, paykit.CapturePaymentIn) (*paykit.CapturePaymentOut, error) {
	return nil, paykit.ErrUnsupportedOperation
}

func (p *wechatProvider) VerifyNotify(ctx context.Context, body []byte, headers map[string]string) (*paykit.NotifyOut, error) {
	if p.notifySvc == nil {
		return nil, fmt.Errorf("wechat notify verifier is not configured")
	}
	tx, err := p.notifySvc.Verify(ctx, body, headers)
	if err != nil {
		return nil, err
	}
	if tx == nil || tx.TradeState == nil || *tx.TradeState != "SUCCESS" {
		return &paykit.NotifyOut{Success: false}, nil
	}
	if tx.OutTradeNo == nil || strings.TrimSpace(*tx.OutTradeNo) == "" {
		return nil, fmt.Errorf("wechat notify missing out_trade_no")
	}
	if tx.TransactionId == nil || strings.TrimSpace(*tx.TransactionId) == "" {
		return nil, fmt.Errorf("wechat notify missing transaction_id")
	}
	amountCents := 0
	if tx.Amount != nil && tx.Amount.Total != nil {
		amountCents = int(*tx.Amount.Total)
	}
	return &paykit.NotifyOut{
		Success:      true,
		OrderNo:      *tx.OutTradeNo,
		ProviderTxID: *tx.TransactionId,
		AmountCents:  amountCents,
	}, nil
}

func (p *wechatProvider) Refund(ctx context.Context, in paykit.RefundIn) (*paykit.RefundOut, error) {
	if p.refundSvc == nil {
		return nil, fmt.Errorf("wechat refund service is not configured")
	}
	if strings.TrimSpace(in.ProviderTxID) == "" {
		return nil, fmt.Errorf("wechat transaction id is required for refund")
	}
	currency := "CNY"
	req := refunddomestic.CreateRequest{
		TransactionId: wechatCore.String(in.ProviderTxID),
		OutRefundNo:   wechatCore.String(in.OrderNo + "-RF"),
		Reason:        wechatCore.String(in.Reason),
		Amount: &refunddomestic.AmountReq{
			Refund:   wechatCore.Int64(int64(in.AmountCents)),
			Total:    wechatCore.Int64(int64(in.AmountCents)),
			Currency: wechatCore.String(currency),
		},
	}
	refund, err := p.refundSvc.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("wechat refund create: %w", err)
	}
	if refund == nil || refund.RefundId == nil || strings.TrimSpace(*refund.RefundId) == "" {
		return nil, fmt.Errorf("wechat refund returned empty refund id")
	}
	success := true
	if refund.Status != nil && *refund.Status == refunddomestic.STATUS_ABNORMAL {
		success = false
	}
	return &paykit.RefundOut{Success: success, ProviderID: *refund.RefundId, AmountCents: in.AmountCents}, nil
}

type wechatNativeAPI struct {
	svc *native.NativeApiService
}

func (a *wechatNativeAPI) Prepay(ctx context.Context, req native.PrepayRequest) (*native.PrepayResponse, error) {
	resp, _, err := a.svc.Prepay(ctx, req)
	return resp, err
}

type wechatRefundAPI struct {
	svc *refunddomestic.RefundsApiService
}

func (a *wechatRefundAPI) Create(ctx context.Context, req refunddomestic.CreateRequest) (*refunddomestic.Refund, error) {
	resp, _, err := a.svc.Create(ctx, req)
	return resp, err
}

type wechatNotifyVerifier struct {
	handler *notify.Handler
}

func (v *wechatNotifyVerifier) Verify(ctx context.Context, body []byte, headers map[string]string) (*payments.Transaction, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://commerce.local/api/v1/payments/wechat/notify", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, value := range headers {
		req.Header.Set(k, value)
	}
	content := new(payments.Transaction)
	if _, err := v.handler.ParseNotifyRequest(ctx, req, content); err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, req.Body)
	return content, nil
}

func normalizeWeChatPEM(key string) string {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, `\n`, "\n")
	return key
}

func truncateDescription(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 127 {
		return s
	}
	return s[:127]
}
