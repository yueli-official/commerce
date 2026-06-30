package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
)

func TestNewWeChatProviderValidatesConfig(t *testing.T) {
	_, err := NewWeChatProvider(WeChatConfig{})
	if err == nil {
		t.Fatal("expected missing config error")
	}
	if !strings.Contains(err.Error(), "merchant id") {
		t.Fatalf("error = %q, want merchant id", err.Error())
	}
}

func TestWeChatCreatePaymentCreatesNativeQRSession(t *testing.T) {
	nativeSvc := &fakeWeChatNativeService{
		prepay: &native.PrepayResponse{CodeUrl: ptrString("weixin://wxpay/bizpayurl?pr=abc")},
	}
	gw := &wechatProvider{
		appID:      "wx-app",
		merchantID: "mch-1",
		notifyURL:  "https://example.com/api/v1/payments/wechat/notify",
		nativeSvc:  nativeSvc,
	}

	out, err := gw.CreatePayment(context.Background(), CreateIn{
		OrderNo:     "ORD-WX-1",
		Subject:     "Digital product",
		AmountCents: 1234,
		Currency:    "CNY",
		NotifyURL:   "https://override.example.com/wechat",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if out.Provider != "wechat" || out.Method != string(CapabilityNativeQR) {
		t.Fatalf("unexpected provider/method: %+v", out)
	}
	if out.SessionID != "ORD-WX-1" || out.QRCode != "weixin://wxpay/bizpayurl?pr=abc" {
		t.Fatalf("unexpected session/qr: %+v", out)
	}
	req := nativeSvc.lastPrepay
	if *req.Appid != "wx-app" || *req.Mchid != "mch-1" || *req.OutTradeNo != "ORD-WX-1" {
		t.Fatalf("unexpected prepay identity fields: %+v", req)
	}
	if *req.Description != "Digital product" {
		t.Fatalf("description = %q", *req.Description)
	}
	if *req.NotifyUrl != "https://override.example.com/wechat" {
		t.Fatalf("notify url = %q", *req.NotifyUrl)
	}
	if *req.Amount.Total != int64(1234) || *req.Amount.Currency != "CNY" {
		t.Fatalf("amount = %+v", req.Amount)
	}
}

func TestWeChatRefundUsesOutTradeNoAndRefundNo(t *testing.T) {
	refundSvc := &fakeWeChatRefundService{
		refund: &refunddomestic.Refund{
			RefundId: ptrString("WX-REFUND-1"),
			Status:   refunddomestic.STATUS_SUCCESS.Ptr(),
		},
	}
	gw := &wechatProvider{
		merchantID: "mch-1",
		refundSvc:  refundSvc,
	}

	out, err := gw.Refund(context.Background(), RefundIn{
		OrderNo:      "ORD-WX-1",
		ProviderTxID: "WX-TX-1",
		AmountCents:  1234,
		Reason:       "customer request",
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if !out.Success || out.ProviderID != "WX-REFUND-1" || out.AmountCents != 1234 {
		t.Fatalf("unexpected refund output: %+v", out)
	}
	req := refundSvc.lastCreate
	if *req.TransactionId != "WX-TX-1" {
		t.Fatalf("transaction id = %q", *req.TransactionId)
	}
	if *req.OutRefundNo != "ORD-WX-1-RF" {
		t.Fatalf("out refund no = %q", *req.OutRefundNo)
	}
	if *req.Reason != "customer request" {
		t.Fatalf("reason = %q", *req.Reason)
	}
	if *req.Amount.Total != int64(1234) || *req.Amount.Refund != int64(1234) || *req.Amount.Currency != "CNY" {
		t.Fatalf("amount = %+v", req.Amount)
	}
}

func TestWeChatNotifyMapsSuccessfulTransaction(t *testing.T) {
	notifySvc := &fakeWeChatNotifyService{tx: &payments.Transaction{
		OutTradeNo:    ptrString("ORD-WX-1"),
		TransactionId: ptrString("WX-TX-1"),
		TradeState:    ptrString("SUCCESS"),
		Amount:        &payments.TransactionAmount{Total: ptrInt64(1234)},
	}}
	gw := &wechatProvider{notifySvc: notifySvc}

	out, err := gw.VerifyNotify(context.Background(), []byte(`{"id":"evt"}`), map[string]string{
		"Wechatpay-Serial":    "serial",
		"Wechatpay-Signature": "sig",
	})
	if err != nil {
		t.Fatalf("VerifyNotify: %v", err)
	}
	if !out.Success || out.OrderNo != "ORD-WX-1" || out.ProviderTxID != "WX-TX-1" || out.AmountCents != 1234 {
		t.Fatalf("unexpected notify output: %+v", out)
	}
	if notifySvc.lastBody != `{"id":"evt"}` {
		t.Fatalf("notify body = %q", notifySvc.lastBody)
	}
}

type fakeWeChatNativeService struct {
	lastPrepay native.PrepayRequest
	prepay     *native.PrepayResponse
	err        error
}

func (f *fakeWeChatNativeService) Prepay(ctx context.Context, req native.PrepayRequest) (*native.PrepayResponse, error) {
	f.lastPrepay = req
	return f.prepay, f.err
}

type fakeWeChatRefundService struct {
	lastCreate refunddomestic.CreateRequest
	refund     *refunddomestic.Refund
	err        error
}

func (f *fakeWeChatRefundService) Create(ctx context.Context, req refunddomestic.CreateRequest) (*refunddomestic.Refund, error) {
	f.lastCreate = req
	return f.refund, f.err
}

type fakeWeChatNotifyService struct {
	lastBody string
	tx       *payments.Transaction
	err      error
}

func (f *fakeWeChatNotifyService) Verify(ctx context.Context, body []byte, headers map[string]string) (*payments.Transaction, error) {
	f.lastBody = string(body)
	return f.tx, f.err
}

func ptrString(v string) *string {
	return &v
}

func ptrInt64(v int64) *int64 {
	return &v
}
