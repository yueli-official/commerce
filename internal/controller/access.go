package controller

import (
	"context"

	"platform/gokit/authjwt"
	"platform/gokit/errs"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/service"
)

// Access handles GET /api/v1/access.
type Access struct {
	svc *service.Service
}

// NewAccess constructs an Access controller.
func NewAccess(svc *service.Service) *Access {
	return &Access{svc: svc}
}

// Entitled handles GET /api/v1/access?siteKey=&externalId= (user JWT required).
func (c *Access) Entitled(ctx context.Context, req *v1.EntitledReq) (*v1.EntitledRes, error) {
	p, ok := authjwt.From(ctx)
	if !ok || p == nil {
		return nil, errs.New(commerceerr.CodeForbidden, "missing principal", nil)
	}

	result, err := c.svc.Entitled(ctx, p.Subject, req.SiteKey, req.ExternalID)
	if err != nil {
		return nil, err
	}

	res := &v1.EntitledRes{
		Entitled: result.Entitled,
		Reason:   result.Reason,
	}
	if result.Required.Kind != "" || result.Required.PriceCents != nil || result.Required.PointsCost != nil {
		res.Required = &v1.RequiredFields{
			Kind:       result.Required.Kind,
			PriceCents: result.Required.PriceCents,
			PointsCost: result.Required.PointsCost,
		}
	}
	return res, nil
}
