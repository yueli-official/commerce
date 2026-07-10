package sitecontext

import (
	"time"

	"github.com/gogf/gf/v2/net/ghttp"

	"platform/gokit/authjwt"
	"platform/gokit/response"
	"platform/services/commerce/internal/commerceerr"
)

const (
	HeaderSiteKey       = "X-Platform-Site-Key"
	HeaderSiteTimestamp = "X-Platform-Site-Timestamp"
	HeaderSiteSignature = "X-Platform-Site-Signature"
)

// Middleware resolves a trusted site after authjwt middleware has optionally
// verified the caller. Browser-controlled request fields are never used here.
func Middleware(resolver *Resolver) func(r *ghttp.Request) {
	return func(r *ghttp.Request) {
		if resolver == nil {
			r.Middleware.Next()
			return
		}

		var resolved Context
		if principal, ok := authjwt.From(r.Context()); ok {
			resolved, _ = resolver.ResolvePrincipal(principal)
		}

		siteKey := r.Header.Get(HeaderSiteKey)
		timestamp := r.Header.Get(HeaderSiteTimestamp)
		signature := r.Header.Get(HeaderSiteSignature)
		if siteKey != "" || timestamp != "" || signature != "" {
			asserted, err := resolver.VerifyAssertion(siteKey, timestamp, signature, time.Now())
			if err != nil || (resolved.SiteKey != "" && resolved.SiteKey != asserted.SiteKey) {
				writeForbidden(r)
				return
			}
			resolved = asserted
		}

		if resolved.SiteKey != "" {
			r.SetCtx(With(r.Context(), resolved))
		}
		r.Middleware.Next()
	}
}

func writeForbidden(r *ghttp.Request) {
	r.Response.ClearBuffer()
	r.Response.WriteHeader(403)
	r.Response.WriteJson(response.Fail(commerceerr.CodeForbidden, "trusted site context is invalid", nil))
}
