// Package server wires the commerce-service HTTP routes onto a GoFrame server.
// Shared by cmd/commerce and integration tests so they exercise the same wiring.
package server

import (
	"github.com/gogf/gf/v2/net/ghttp"

	"platform/gokit/authjwt"
	"platform/gokit/ghttpx"
	"platform/gokit/response"
	"platform/services/commerce/internal/controller"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/gateway"
	"platform/services/commerce/internal/service"
)

// Deps are the wiring dependencies for the commerce server.
type Deps struct {
	Verifier  *authjwt.Verifier
	DB        *dao.PG
	Registry  gateway.Registry
	NotifyURL string                // base URL for the alipay notify callback
	ReturnURL string                // URL the buyer is sent to after paying
	DevSettle bool                  // when true, register the /dev/orders/{orderNo}/settle endpoint
	Checkin   service.CheckinConfig // daily check-in reward curve
}

// Configure mounts the commerce-service routes onto s.
func Configure(s *ghttp.Server, d Deps) {
	svc := service.New(d.DB, d.Checkin)

	// ── Public: liveness ────────────────────────────────────────────────────
	s.Group("/", func(grp *ghttp.RouterGroup) {
		grp.Middleware(ghttpx.Middleware)
		grp.GET("/healthz", func(r *ghttp.Request) {
			r.Response.WriteJson(response.OK(map[string]any{"status": "up"}))
		})
	})

	// ── Public: Alipay async notify (no JWT; raw plaintext response) ────────
	// The notify handler writes "success"/"fail" directly — ghttpx envelope
	// is present for trace injection but will leave the response untouched
	// because the handler writes raw bytes before Middleware.Next() returns.
	if d.Registry != nil {
		if gw, ok := d.Registry["alipay"]; ok {
			notifyCtrl := controller.NewNotify(gw, svc)
			s.Group("/", func(grp *ghttp.RouterGroup) {
				grp.Middleware(ghttpx.Middleware)
				grp.POST("/api/v1/payments/alipay/notify", notifyCtrl.Handle)
			})
		}
	}

	// ── Protected: JWT-required routes ──────────────────────────────────────
	orderCtrl := controller.NewOrder(svc, d.Registry, d.NotifyURL, d.ReturnURL)
	accessCtrl := controller.NewAccess(svc)
	checkinCtrl := controller.NewCheckin(svc)
	creditsCtrl := controller.NewCredits(svc)
	s.Group("/", func(grp *ghttp.RouterGroup) {
		grp.Middleware(ghttpx.Middleware, authjwt.Middleware(d.Verifier))
		grp.Bind(orderCtrl)
		grp.Bind(accessCtrl)
		grp.Bind(checkinCtrl)
		grp.Bind(creditsCtrl)
	})

	// ── Dev-only: settle seam (conditionally registered) ────────────────────
	if d.DevSettle {
		settleCtrl := controller.NewDevSettle(svc)
		s.Group("/", func(grp *ghttp.RouterGroup) {
			grp.Middleware(ghttpx.Middleware)
			grp.Bind(settleCtrl)
		})
	}
}
