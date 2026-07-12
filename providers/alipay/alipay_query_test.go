package alipay

import (
	"strings"
	"testing"

	smartalipay "github.com/smartwalle/alipay/v3"
)

func TestQueryOutFromTradeQueryPaid(t *testing.T) {
	out, err := queryOutFromTradeQuery(&smartalipay.TradeQueryRsp{
		OutTradeNo:  "ORD-QUERY-1",
		TradeNo:     "ALI-TX-1",
		TradeStatus: smartalipay.TradeStatusSuccess,
		TotalAmount: "1.00",
	}, 100)
	if err != nil {
		t.Fatalf("queryOutFromTradeQuery: %v", err)
	}
	if !out.Success || out.OrderNo != "ORD-QUERY-1" || out.ProviderTxID != "ALI-TX-1" || out.AmountCents != 100 {
		t.Fatalf("unexpected query output: %+v", out)
	}
}

func TestQueryOutFromTradeQueryWaitBuyerPay(t *testing.T) {
	out, err := queryOutFromTradeQuery(&smartalipay.TradeQueryRsp{
		OutTradeNo:  "ORD-QUERY-2",
		TradeStatus: smartalipay.TradeStatusWaitBuyerPay,
		TotalAmount: "1.00",
	}, 100)
	if err != nil {
		t.Fatalf("queryOutFromTradeQuery: %v", err)
	}
	if out.Success {
		t.Fatalf("waiting payment query must not settle: %+v", out)
	}
}

func TestQueryOutFromTradeQueryAmountMismatch(t *testing.T) {
	_, err := queryOutFromTradeQuery(&smartalipay.TradeQueryRsp{
		OutTradeNo:  "ORD-QUERY-3",
		TradeNo:     "ALI-TX-3",
		TradeStatus: smartalipay.TradeStatusSuccess,
		TotalAmount: "2.00",
	}, 100)
	if err == nil {
		t.Fatal("expected amount mismatch error")
	}
	if !strings.Contains(err.Error(), "amount mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHealthQueryAcceptsAuthenticatedMissingOrder(t *testing.T) {
	if !healthQuerySucceeded(&smartalipay.TradeQueryRsp{Error: smartalipay.Error{SubCode: "ACQ.TRADE_NOT_EXIST"}}) {
		t.Fatal("authenticated missing synthetic order should prove provider health")
	}
	if healthQuerySucceeded(&smartalipay.TradeQueryRsp{Error: smartalipay.Error{Code: "40004", SubCode: "ACQ.INVALID_PARAMETER"}}) {
		t.Fatal("unexpected provider error accepted as healthy")
	}
}
