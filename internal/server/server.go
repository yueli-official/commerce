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

	// ── Public: payment async notify (no JWT; provider-native response) ─────
	// Notify handlers write raw provider responses directly, bypassing the
	// standard JSON envelope.
	if d.Registry != nil {
		if gw, ok := d.Registry["alipay"]; ok {
			notifyCtrl := controller.NewNotify("alipay", gw, svc)
			s.Group("/", func(grp *ghttp.RouterGroup) {
				grp.Middleware(ghttpx.Middleware)
				grp.POST("/api/v1/payments/alipay/notify", notifyCtrl.Handle)
			})
		}
		if gw, ok := d.Registry["wechat"]; ok {
			notifyCtrl := controller.NewNotify("wechat", gw, svc)
			s.Group("/", func(grp *ghttp.RouterGroup) {
				grp.Middleware(ghttpx.Middleware)
				grp.POST("/api/v1/payments/wechat/notify", notifyCtrl.Handle)
			})
		}
	}

	// ── Public/optional-auth: virtual-goods checkout ─────────────────────────
	// Guest buyers identify by email; logged-in buyers get their subject from an
	// optional Bearer token injected by the app BFF.
	checkoutCtrl := controller.NewCheckout(svc, d.Registry, d.NotifyURL)
	s.Group("/", func(grp *ghttp.RouterGroup) {
		grp.Middleware(ghttpx.Middleware, authjwt.OptionalMiddleware(d.Verifier))
		grp.Bind(checkoutCtrl)
	})

	// ── Protected: JWT-required routes ──────────────────────────────────────
	orderCtrl := controller.NewOrder(svc, d.Registry, d.NotifyURL, d.ReturnURL)
	accessCtrl := controller.NewAccess(svc)
	checkinCtrl := controller.NewCheckin(svc)
	creditsCtrl := controller.NewCredits(svc)
	adminOrderCtrl := controller.NewAdminOrder(svc)
	s.Group("/", func(grp *ghttp.RouterGroup) {
		grp.Middleware(ghttpx.Middleware, authjwt.Middleware(d.Verifier))
		grp.Bind(orderCtrl)
		grp.Bind(accessCtrl)
		grp.Bind(checkinCtrl)
		grp.Bind(creditsCtrl)
		grp.Bind(adminOrderCtrl)
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
