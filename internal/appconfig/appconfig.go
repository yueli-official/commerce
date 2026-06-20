// Package appconfig builds runtime config objects from GoFrame config
// (manifest/config/config.yaml + GF_* env overrides).
package appconfig

import (
	"context"

	"github.com/gogf/gf/v2/frame/g"
)

// JWKS holds the IdP key/issuer config for the authjwt verifier.
type JWKS struct {
	URL      string
	Issuer   string
	Audience string
}

// LoadJWKS reads commerce.jwks.* from the GoFrame config.
func LoadJWKS(ctx context.Context) JWKS {
	return JWKS{
		URL:      g.Cfg().MustGet(ctx, "commerce.jwks.url").String(),
		Issuer:   g.Cfg().MustGet(ctx, "commerce.jwks.issuer").String(),
		Audience: g.Cfg().MustGet(ctx, "commerce.jwks.audience").String(),
	}
}
