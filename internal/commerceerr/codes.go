package commerceerr

// ProductNotFound 返回商品不存在错误。
func ProductNotFound(siteKey, externalID string) error {
	return mapped(CodeProductNotFound, map[string]any{
		"site_key": siteKey, "external_id": externalID,
	})
}

// OrderNotFound 返回订单不存在错误。
func OrderNotFound(orderNo string) error {
	return mapped(CodeOrderNotFound, map[string]any{"order_no": orderNo})
}

// OrderInvalidState 返回不允许的订单状态转换错误。
func OrderInvalidState(from, to string) error {
	return mapped(CodeOrderInvalidState, map[string]any{"from": from, "to": to})
}

// Forbidden 返回调用方无权访问订单或管理能力的错误。
func Forbidden() error {
	return mapped(CodeForbidden, nil)
}

func SiteContextForbidden() error {
	return mapped(CodeForbidden, nil)
}

// GatewayFailed 返回支付网关调用失败错误。Provider 细节应由调用方单独记录，不进入公共响应。
func GatewayFailed(_ string) error {
	return mapped(CodeGatewayFailed, nil)
}

// NotifyInvalid 返回支付通知或支付结果无效错误。
func NotifyInvalid(_ string) error {
	return mapped(CodeNotifyInvalid, nil)
}

// InsufficientPoints 返回积分余额不足错误。
func InsufficientPoints(pointsCost int) error {
	return mapped(CodeInsufficientPoints, map[string]any{"pointsCost": pointsCost})
}

// InvalidRequest 返回请求业务参数无效错误。
func InvalidRequest(_ string) error {
	return mapped(CodeInvalidRequest, nil)
}

func CapabilityNotFound(key string) error {
	return mapped(CodeCapabilityNotFound, map[string]any{"key": key})
}

func ProviderNotFound(key string) error {
	return mapped(CodeProviderNotFound, map[string]any{"key": key})
}

func HealthCheckRateLimited(key string) error {
	return mapped(CodeHealthCheckRateLimited, map[string]any{"key": key})
}
