package gateway_test

import (
	"context"
	"net/url"
	"testing"

	"platform/services/commerce/internal/gateway"
)

func TestWeChatStubProviderCreatesNativeQRSession(t *testing.T) {
	gw := gateway.NewWeChatStubProvider()
	out, err := gw.CreatePayment(context.Background(), gateway.CreateIn{OrderNo: "ORD-WX"})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if out.Provider != "wechat" {
		t.Fatalf("provider = %q, want wechat", out.Provider)
	}
	if out.Method != string(gateway.CapabilityNativeQR) {
		t.Fatalf("method = %q, want native_qr", out.Method)
	}
	if out.QRCode == "" {
		t.Fatal("expected QR payload")
	}
}

func TestAlipayStubProviderCreatesLocalMockPayURL(t *testing.T) {
	gw := gateway.NewAlipayStubProvider()
	out, err := gw.CreatePayment(context.Background(), gateway.CreateIn{
		OrderNo:     "ORD-ALI",
		Subject:     "Starter Kit",
		AmountCents: 9900,
		Currency:    "CNY",
		ReturnURL:   "http://localhost:3004/checkout/success",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	u, err := url.Parse(out.PayURL)
	if err != nil {
		t.Fatalf("parse pay url: %v", err)
	}
	if u.Scheme != "http" || u.Host != "localhost:3004" || u.Path != "/checkout/mock-pay" {
		t.Fatalf("pay url = %q, want local mock pay page", out.PayURL)
	}
	q := u.Query()
	if q.Get("orderNo") != "ORD-ALI" || q.Get("provider") != "alipay" || q.Get("amountCents") != "9900" {
		t.Fatalf("unexpected query: %v", q)
	}
}

func TestPayPalStubProviderCreatesBrowserSessionAndCapture(t *testing.T) {
	gw := gateway.NewPayPalStubProvider()
	out, err := gw.CreatePayment(context.Background(), gateway.CreateIn{OrderNo: "ORD-PP"})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if out.Provider != "paypal" {
		t.Fatalf("provider = %q, want paypal", out.Provider)
	}
	if out.Method != string(gateway.CapabilityBrowserButton) {
		t.Fatalf("method = %q, want browser_button", out.Method)
	}
	if out.SessionID == "" || out.ClientToken == "" {
		t.Fatal("expected session and client token")
	}
	capture, err := gw.CapturePayment(context.Background(), gateway.CapturePaymentIn{OrderNo: "ORD-PP", SessionID: out.SessionID})
	if err != nil {
		t.Fatalf("CapturePayment: %v", err)
	}
	if !capture.Success || capture.OrderNo != "ORD-PP" {
		t.Fatalf("unexpected capture result: %+v", capture)
	}
}
