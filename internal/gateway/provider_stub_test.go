package gateway_test

import (
	"context"
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
