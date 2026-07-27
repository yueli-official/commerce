package commerceerr_test

import (
	"net/http"
	"testing"

	"github.com/yueli-official/commerce/internal/commerceerr"
)

func TestCodesRegistered(t *testing.T) {
	cases := map[string]int{
		commerceerr.CodeProductNotFound:        http.StatusNotFound,
		commerceerr.CodeOrderNotFound:          http.StatusNotFound,
		commerceerr.CodeOrderInvalidState:      http.StatusConflict,
		commerceerr.CodeForbidden:              http.StatusForbidden,
		commerceerr.CodeGatewayFailed:          http.StatusBadGateway,
		commerceerr.CodeNotifyInvalid:          http.StatusBadRequest,
		commerceerr.CodeInsufficientPoints:     http.StatusPaymentRequired,
		commerceerr.CodeInvalidRequest:         http.StatusBadRequest,
		commerceerr.CodeCapabilityNotFound:     http.StatusNotFound,
		commerceerr.CodeProviderNotFound:       http.StatusNotFound,
		commerceerr.CodeHealthCheckRateLimited: http.StatusTooManyRequests,
	}
	for code, want := range cases {
		descriptor, ok := commerceerr.DescriptorForCode(code)
		if !ok {
			t.Errorf("DescriptorForCode(%q) is missing", code)
			continue
		}
		if got := descriptor.Kind().Status(); got != want {
			t.Errorf("Status(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestConstructorsCarryCode(t *testing.T) {
	cases := map[string]error{
		commerceerr.CodeProductNotFound:        commerceerr.ProductNotFound("shop", "product"),
		commerceerr.CodeOrderNotFound:          commerceerr.OrderNotFound("order"),
		commerceerr.CodeOrderInvalidState:      commerceerr.OrderInvalidState("pending", "paid"),
		commerceerr.CodeForbidden:              commerceerr.Forbidden(),
		commerceerr.CodeGatewayFailed:          commerceerr.GatewayFailed("gateway"),
		commerceerr.CodeNotifyInvalid:          commerceerr.NotifyInvalid("notify"),
		commerceerr.CodeInsufficientPoints:     commerceerr.InsufficientPoints(10),
		commerceerr.CodeInvalidRequest:         commerceerr.InvalidRequest("request"),
		commerceerr.CodeCapabilityNotFound:     commerceerr.CapabilityNotFound("payment"),
		commerceerr.CodeProviderNotFound:       commerceerr.ProviderNotFound("alipay"),
		commerceerr.CodeHealthCheckRateLimited: commerceerr.HealthCheckRateLimited("alipay"),
	}
	for want, err := range cases {
		value, ok := commerceerr.Resolve(err)
		if !ok {
			t.Errorf("%s did not resolve", want)
			continue
		}
		if value.Code != want {
			t.Errorf("code = %q, want %q", value.Code, want)
		}
	}
}
