// Package commerceerr 声明 Commerce 不可变的公共 Problem 错误合同。
// Foundation 负责校验和协议映射，Commerce 负责错误码、状态、参数与公开类型 URI。
package commerceerr

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/yueli-official/foundation/go/problem"
)

const (
	CodeProductNotFound        = "commerce.product_not_found"
	CodeOrderNotFound          = "commerce.order_not_found"
	CodeOrderInvalidState      = "commerce.order_invalid_state"
	CodeForbidden              = "commerce.forbidden"
	CodeGatewayFailed          = "commerce.gateway_failed"
	CodeNotifyInvalid          = "commerce.notify_invalid"
	CodeInsufficientPoints     = "commerce.insufficient_points"
	CodeInvalidRequest         = "commerce.invalid_request"
	CodeCapabilityNotFound     = "commerce.capability_not_found"
	CodeProviderNotFound       = "commerce.provider_not_found"
	CodeHealthCheckRateLimited = "commerce.health_check_rate_limited"
)

var (
	DescriptorRateLimited = descriptor("common.rate_limited", http.StatusTooManyRequests)
	DescriptorValidation  = descriptor("common.validation_failed", http.StatusBadRequest)
	DescriptorInternal    = descriptor("common.internal", http.StatusInternalServerError)

	descriptors = map[string]problem.Descriptor{
		CodeProductNotFound:        descriptor(CodeProductNotFound, http.StatusNotFound),
		CodeOrderNotFound:          descriptor(CodeOrderNotFound, http.StatusNotFound),
		CodeOrderInvalidState:      descriptor(CodeOrderInvalidState, http.StatusConflict),
		CodeForbidden:              descriptor(CodeForbidden, http.StatusForbidden),
		CodeGatewayFailed:          descriptor(CodeGatewayFailed, http.StatusBadGateway),
		CodeNotifyInvalid:          descriptor(CodeNotifyInvalid, http.StatusBadRequest),
		CodeInsufficientPoints:     descriptor(CodeInsufficientPoints, http.StatusPaymentRequired),
		CodeInvalidRequest:         descriptor(CodeInvalidRequest, http.StatusBadRequest),
		CodeCapabilityNotFound:     descriptor(CodeCapabilityNotFound, http.StatusNotFound),
		CodeProviderNotFound:       descriptor(CodeProviderNotFound, http.StatusNotFound),
		CodeHealthCheckRateLimited: descriptor(CodeHealthCheckRateLimited, http.StatusTooManyRequests),
	}
)

func descriptor(code string, status int) problem.Descriptor {
	return problem.MustDescriptor(
		problem.MustKind(code, status),
		"https://errors.yueli.dev/problems/"+code,
	)
}

func DescriptorForCode(code string) (problem.Descriptor, bool) {
	value, ok := descriptors[code]
	return value, ok
}

type CatalogEntry struct {
	Code   string `json:"code"`
	Status int    `json:"status"`
}

// Catalog 返回按错误码排序的 Commerce 公共错误合同副本。
func Catalog() []CatalogEntry {
	result := make([]CatalogEntry, 0, len(descriptors))
	for code, value := range descriptors {
		result = append(result, CatalogEntry{
			Code:   code,
			Status: value.Kind().Status(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Code < result[j].Code
	})
	return result
}

func Resolve(err error) (problem.Problem, bool) {
	value, ok, resolveErr := problem.FromError(err, "commerce-error-inspection")
	return value, ok && resolveErr == nil
}

func mapped(code string, params problem.Parameters) error {
	value, ok := DescriptorForCode(code)
	if !ok {
		return fmt.Errorf("commerce public error code is not declared: %s", code)
	}
	result, err := problem.NewError(value, params)
	if err != nil {
		return fmt.Errorf("commerce public error %s: %w", code, err)
	}
	return result
}
