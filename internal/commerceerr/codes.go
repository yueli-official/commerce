// Package commerceerr declares the commerce-service error codes (namespace commerce.*)
// and their HTTP status, registered with the shared gokit/errs catalog.
package commerceerr

import (
	"net/http"

	"platform/gokit/errs"
)

var (
	// CodeProductNotFound is returned when the requested product does not exist.
	CodeProductNotFound = errs.Register("commerce.product_not_found", http.StatusNotFound)

	// CodeOrderNotFound is returned when the requested order does not exist.
	CodeOrderNotFound = errs.Register("commerce.order_not_found", http.StatusNotFound)

	// CodeOrderInvalidState is returned when a state transition is not allowed.
	CodeOrderInvalidState = errs.Register("commerce.order_invalid_state", http.StatusConflict)

	// CodeForbidden is returned when an order does not belong to the requesting sub.
	CodeForbidden = errs.Register("commerce.forbidden", http.StatusForbidden)

	// CodeGatewayFailed is returned when the payment gateway call fails.
	// Only a trimmed summary is returned — never the provider stack or keys.
	CodeGatewayFailed = errs.Register("commerce.gateway_failed", http.StatusBadGateway)

	// CodeNotifyInvalid is returned on signature failure, parse error, or amount mismatch.
	CodeNotifyInvalid = errs.Register("commerce.notify_invalid", http.StatusBadRequest)

	// CodeInsufficientPoints is returned when a points redemption exceeds the balance.
	CodeInsufficientPoints = errs.Register("commerce.insufficient_points", http.StatusPaymentRequired)

	// CodeInvalidRequest is returned for malformed request input (e.g. a paid order
	// missing price/currency, or a points order missing pointsCost).
	CodeInvalidRequest         = errs.Register("commerce.invalid_request", http.StatusBadRequest)
	CodeCapabilityNotFound     = errs.Register("commerce.capability_not_found", http.StatusNotFound)
	CodeProviderNotFound       = errs.Register("commerce.provider_not_found", http.StatusNotFound)
	CodeHealthCheckRateLimited = errs.Register("commerce.health_check_rate_limited", http.StatusTooManyRequests)
)

// ProductNotFound returns a Coded error for a missing product.
func ProductNotFound(siteKey, externalID string) *errs.Coded {
	return errs.New(CodeProductNotFound, "product not found",
		map[string]any{"site_key": siteKey, "external_id": externalID})
}

// OrderNotFound returns a Coded error for a missing order.
func OrderNotFound(orderNo string) *errs.Coded {
	return errs.New(CodeOrderNotFound, "order not found", map[string]any{"order_no": orderNo})
}

// OrderInvalidState returns a Coded error for an illegal state transition.
func OrderInvalidState(from, to string) *errs.Coded {
	return errs.New(CodeOrderInvalidState, "illegal order state transition",
		map[string]any{"from": from, "to": to})
}

// Forbidden returns a Coded error when the caller does not own the order.
func Forbidden() *errs.Coded {
	return errs.New(CodeForbidden, "order does not belong to this subscriber", nil)
}

func SiteContextForbidden() *errs.Coded {
	return errs.New(CodeForbidden, "trusted site context is required", nil)
}

// GatewayFailed returns a Coded error for a payment gateway failure.
func GatewayFailed(summary string) *errs.Coded {
	return errs.New(CodeGatewayFailed, summary, nil)
}

// NotifyInvalid returns a Coded error for an invalid payment notification.
func NotifyInvalid(detail string) *errs.Coded {
	return errs.New(CodeNotifyInvalid, detail, nil)
}

// InsufficientPoints returns a Coded error when the balance can't cover a redemption.
func InsufficientPoints(pointsCost int) *errs.Coded {
	return errs.New(CodeInsufficientPoints, "insufficient points balance",
		map[string]any{"pointsCost": pointsCost})
}

// InvalidRequest returns a Coded error for malformed request input.
func InvalidRequest(detail string) *errs.Coded {
	return errs.New(CodeInvalidRequest, detail, nil)
}

func CapabilityNotFound(key string) *errs.Coded {
	return errs.New(CodeCapabilityNotFound, "commerce capability not found", map[string]any{"key": key})
}

func ProviderNotFound(key string) *errs.Coded {
	return errs.New(CodeProviderNotFound, "commerce payment provider not found", map[string]any{"key": key})
}

func HealthCheckRateLimited(key string) *errs.Coded {
	return errs.New(CodeHealthCheckRateLimited, "commerce payment provider health check rate limit exceeded", map[string]any{"key": key})
}
