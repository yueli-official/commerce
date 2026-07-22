// Package server wires the commerce-service HTTP routes onto a GoFrame server.
// Shared by cmd/commerce and integration tests so they exercise the same wiring.
package server

import (
	"github.com/gogf/gf/v2/net/ghttp"

	foundationauth "github.com/yueli-official/foundation/go/auth"
	"platform/gokit/authhttp"
	"platform/gokit/capability"
	"platform/gokit/ghttpx"
	"platform/gokit/healthcheck"
	"platform/paykit"
	"platform/services/commerce/internal/controller"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/paymentcap"
	"platform/services/commerce/internal/service"
	"platform/services/commerce/internal/sitecontext"
)

// Deps are the wiring dependencies for the commerce server.
type Deps struct {
	Verifier             *foundationauth.Verifier
	DB                   *dao.PG
	Registry             paykit.Registry
	NotifyURL            string                // base URL for the alipay notify callback
	ReturnURL            string                // URL the buyer is sent to after paying
	DevSettle            bool                  // when true, register the /dev/orders/{orderNo}/settle endpoint
	Checkin              service.CheckinConfig // daily check-in reward curve
	Delivery             service.DeliveryConfig
	Mailer               service.DeliveryMailer
	Asset                service.AssetDeliveryClient
	CurrentDelivery      service.CurrentDeliveryResolver
	CurrentCheckout      service.CurrentCheckoutItemResolver
	SiteContext          *sitecontext.Resolver
	Capabilities         *paymentcap.Registry
	CapabilityScope      string
	CapabilityProbeScope string
	CapabilityService    capability.ServiceMetadata
}

// Configure mounts the commerce-service routes onto s.
func Configure(s *ghttp.Server, d Deps) {
	apiMiddleware := ghttpx.NewMiddleware(ghttpx.MustRateLimiterFromEnvironment(), ghttpx.ForwardedClientIPKey)
	s.Use(ghttpx.TraceRouteMiddleware)
	currentCheckout := d.CurrentCheckout
	if currentCheckout == nil && d.CurrentDelivery != nil {
		if resolver, ok := d.CurrentDelivery.(service.CurrentCheckoutItemResolver); ok {
			currentCheckout = resolver
		}
	}
	svc := service.New(
		d.DB,
		d.Checkin,
		service.WithDeliveryConfig(d.Delivery),
		service.WithDeliveryMailer(d.Mailer),
		service.WithAssetDeliveryClient(d.Asset),
		service.WithCurrentDeliveryResolver(d.CurrentDelivery),
		service.WithCurrentCheckoutItemResolver(currentCheckout),
	)

	// ── Public: liveness ────────────────────────────────────────────────────
	s.Group("/", func(grp *ghttp.RouterGroup) {
		grp.Middleware(apiMiddleware)
		grp.GET("/healthz", func(r *ghttp.Request) {
			r.Response.WriteJson(map[string]any{"status": "up"})
		})
		grp.GET("/readyz", healthcheck.Handler(map[string]healthcheck.Check{"database": healthcheck.Database}))
	})

	if d.Capabilities != nil {
		capabilityCtrl := controller.NewCapability(svc, d.Capabilities, d.CapabilityService, d.CapabilityScope, d.CapabilityProbeScope)
		s.Group("/", func(grp *ghttp.RouterGroup) {
			grp.Middleware(apiMiddleware, authhttp.Required(d.Verifier))
			grp.Bind(capabilityCtrl)
		})
	}

	// ── Public: payment async notify (no JWT; provider-native response) ─────
	// Notify handlers write raw provider responses directly, bypassing the
	// standard JSON envelope.
	if d.Registry != nil {
		if gw, ok := d.Registry["alipay"]; ok {
			notifyCtrl := controller.NewNotify("alipay", gw, svc)
			s.Group("/", func(grp *ghttp.RouterGroup) {
				grp.Middleware(apiMiddleware)
				grp.POST("/api/v1/payments/alipay/notify", notifyCtrl.Handle)
			})
		}
		if gw, ok := d.Registry["wechat"]; ok {
			notifyCtrl := controller.NewNotify("wechat", gw, svc)
			s.Group("/", func(grp *ghttp.RouterGroup) {
				grp.Middleware(apiMiddleware)
				grp.POST("/api/v1/payments/wechat/notify", notifyCtrl.Handle)
			})
		}
	}

	// ── Public/optional-auth: virtual-goods checkout ─────────────────────────
	// Guest buyers identify by email; logged-in buyers get their subject from an
	// optional Bearer token injected by the app BFF.
	checkoutCtrl := controller.NewCheckout(svc, d.Registry, d.NotifyURL, d.ReturnURL, d.SiteContext)
	paymentConfigCtrl := controller.NewPaymentConfig(svc, d.Registry)
	s.Group("/", func(grp *ghttp.RouterGroup) {
		grp.Middleware(apiMiddleware, authhttp.Optional(d.Verifier), sitecontext.Middleware(d.SiteContext))
		grp.Bind(checkoutCtrl)
		grp.Bind(paymentConfigCtrl)
	})

	// ── Protected: JWT-required routes ──────────────────────────────────────
	accessCtrl := controller.NewAccess(svc, d.SiteContext)
	checkinCtrl := controller.NewCheckin(svc)
	creditsCtrl := controller.NewCredits(svc)
	adminOrderCtrl := controller.NewAdminOrder(svc, d.Registry)
	s.Group("/", func(grp *ghttp.RouterGroup) {
		grp.Middleware(apiMiddleware, authhttp.Required(d.Verifier), sitecontext.Middleware(d.SiteContext))
		grp.Bind(accessCtrl)
		grp.Bind(checkinCtrl)
		grp.Bind(creditsCtrl)
		grp.Bind(adminOrderCtrl)
	})

	// ── Dev-only: settle seam (conditionally registered) ────────────────────
	if d.DevSettle {
		settleCtrl := controller.NewDevSettle(svc)
		s.Group("/", func(grp *ghttp.RouterGroup) {
			grp.Middleware(apiMiddleware)
			grp.Bind(settleCtrl)
		})
	}
}
