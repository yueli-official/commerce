package controller

import (
	"context"

	foundationauth "github.com/yueli-official/foundation/go/auth"
	v1 "github.com/yueli-official/commerce/api/v1"
	"github.com/yueli-official/commerce/internal/commerceerr"
	"github.com/yueli-official/commerce/internal/service"
	"github.com/yueli-official/commerce/internal/sitecontext"
)

// Access handles GET /api/v1/access.
type Access struct {
	svc   *service.Service
	sites *sitecontext.Resolver
}

// NewAccess constructs an Access controller.
func NewAccess(svc *service.Service, sites ...*sitecontext.Resolver) *Access {
	var siteResolver *sitecontext.Resolver
	if len(sites) > 0 {
		siteResolver = sites[0]
	}
	return &Access{svc: svc, sites: siteResolver}
}

// Entitled handles GET /api/v1/access?siteKey=&externalId= (user JWT required).
func (c *Access) Entitled(ctx context.Context, req *v1.EntitledReq) (*v1.EntitledRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}

	siteKey := req.SiteKey
	if c.sites != nil {
		var err error
		siteKey, err = c.sites.RequireSite(ctx, siteKey)
		if err != nil {
			return nil, commerceerr.SiteContextForbidden()
		}
	}
	result, err := c.svc.Entitled(ctx, p.Subject, siteKey, req.ExternalID)
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
