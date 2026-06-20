// Package server wires the commerce-service HTTP routes onto a GoFrame server.
// Shared by cmd/commerce and integration tests so they exercise the same wiring.
package server

import (
	"github.com/gogf/gf/v2/net/ghttp"

	"platform/gokit/authjwt"
	"platform/gokit/ghttpx"
	"platform/gokit/response"
	"platform/services/commerce/internal/dao"
)

// Deps are the wiring dependencies for the commerce server.
type Deps struct {
	Verifier *authjwt.Verifier
	DB       *dao.PG
}

// Configure mounts the commerce-service routes onto s.
// Task 1: only the public healthz liveness endpoint is registered.
func Configure(s *ghttp.Server, d Deps) {
	s.Group("/", func(grp *ghttp.RouterGroup) {
		grp.Middleware(ghttpx.Middleware)
		grp.GET("/healthz", func(r *ghttp.Request) {
			r.Response.WriteJson(response.OK(map[string]any{"status": "up"}))
		})
	})
}
