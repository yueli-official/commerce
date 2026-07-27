package controller

import (
	"context"

	"github.com/yueli-official/commerce/paykit"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/service"
)

type PaymentConfig struct {
	svc      *service.Service
	registry paykit.Registry
}

func NewPaymentConfig(svc *service.Service, reg paykit.Registry) *PaymentConfig {
	return &PaymentConfig{svc: svc, registry: reg}
}

func (c *PaymentConfig) PublicPaymentMethods(ctx context.Context, req *v1.PublicPaymentMethodsReq) (*v1.PublicPaymentMethodsRes, error) {
	methods, err := c.svc.PaymentMethods(ctx)
	if err != nil {
		return nil, err
	}
	res := &v1.PublicPaymentMethodsRes{}
	for _, method := range methods {
		registered := c.isRegistered(method.Provider)
		if method.Enabled && registered {
			res.Methods = append(res.Methods, paymentMethodView(method, registered))
		}
	}
	return res, nil
}

func (c *PaymentConfig) AdminPaymentMethods(ctx context.Context, req *v1.AdminPaymentMethodsReq) (*v1.AdminPaymentMethodsRes, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	methods, err := c.svc.PaymentMethods(ctx)
	if err != nil {
		return nil, err
	}
	return &v1.AdminPaymentMethodsRes{Methods: c.adminViews(methods)}, nil
}

func (c *PaymentConfig) SavePaymentMethods(ctx context.Context, req *v1.AdminSavePaymentMethodsReq) (*v1.AdminSavePaymentMethodsRes, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	inputs := make([]service.PaymentMethodInput, 0, len(req.Methods))
	for _, method := range req.Methods {
		inputs = append(inputs, service.PaymentMethodInput{
			Provider:  method.Provider,
			Label:     method.Label,
			Enabled:   method.Enabled,
			SortOrder: method.SortOrder,
		})
	}
	methods, err := c.svc.SavePaymentMethods(ctx, inputs)
	if err != nil {
		return nil, err
	}
	return &v1.AdminSavePaymentMethodsRes{Methods: c.adminViews(methods)}, nil
}

func (c *PaymentConfig) adminViews(methods []service.PaymentMethodConfig) []v1.PaymentMethodView {
	views := make([]v1.PaymentMethodView, 0, len(methods))
	for _, method := range methods {
		views = append(views, paymentMethodView(method, c.isRegistered(method.Provider)))
	}
	return views
}

func (c *PaymentConfig) isRegistered(provider string) bool {
	if c.registry == nil {
		return false
	}
	_, ok := c.registry[provider]
	return ok
}

func paymentMethodView(method service.PaymentMethodConfig, registered bool) v1.PaymentMethodView {
	return v1.PaymentMethodView{
		Provider:    method.Provider,
		Label:       method.Label,
		Method:      method.Method,
		Enabled:     method.Enabled,
		Registered:  registered,
		SortOrder:   method.SortOrder,
		Description: method.Description,
	}
}
