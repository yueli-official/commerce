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

// Alipay holds the Alipay payment provider config.
type Alipay struct {
	AppID           string
	PrivateKey      string
	AlipayPublicKey string
	Sandbox         bool
	NotifyURL       string
	ReturnURL       string
}

// LoadAlipay reads commerce.alipay.* from the GoFrame config.
func LoadAlipay(ctx context.Context) Alipay {
	return Alipay{
		AppID:           g.Cfg().MustGet(ctx, "commerce.alipay.app_id").String(),
		PrivateKey:      g.Cfg().MustGet(ctx, "commerce.alipay.private_key").String(),
		AlipayPublicKey: g.Cfg().MustGet(ctx, "commerce.alipay.alipay_public_key").String(),
		Sandbox:         g.Cfg().MustGet(ctx, "commerce.alipay.sandbox").Bool(),
		NotifyURL:       g.Cfg().MustGet(ctx, "commerce.alipay.notify_url").String(),
		ReturnURL:       g.Cfg().MustGet(ctx, "commerce.alipay.return_url").String(),
	}
}
