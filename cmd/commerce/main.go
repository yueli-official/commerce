// Command commerce is the payment and entitlement service.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gctx"

	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"platform/gokit/authsetup"
	"platform/gokit/observability"
	"platform/gokit/openapiexport"
	"platform/gokit/postgresdb"
	"platform/gokit/webhooksetup"
	"platform/paykit"
	payalipay "platform/paykit/providers/alipay"
	paypal "platform/paykit/providers/paypal"
	wechat "platform/paykit/providers/wechat"
	"platform/services/commerce/internal/appconfig"
	"platform/services/commerce/internal/commercewebhook"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/server"
	commerceservice "platform/services/commerce/internal/service"
)

func main() {
	ctx := gctx.New()
	shutdown, err := observability.StartFromEnvironment(ctx, "commerce-api")
	if err != nil {
		panic(err)
	}
	defer observability.ShutdownWithTimeout(shutdown)

	jw := appconfig.LoadJWKS(ctx)
	verifier, err := authsetup.NewRemoteVerifier(authsetup.RemoteVerifierConfig{
		JWKSURL: jw.URL, Issuer: jw.Issuer, Audience: jw.Audience,
		AllowLoopbackHTTP: jw.AllowLoopbackHTTP,
	})
	if err != nil {
		panic(err)
	}

	var (
		alipayCfg = appconfig.LoadAlipay(ctx)
		paypalCfg = appconfig.LoadPayPal(ctx)
		wechatCfg = appconfig.LoadWeChat(ctx)
	)
	reg, err := buildGatewayRegistry(alipayCfg, paypalCfg, wechatCfg)
	if err != nil {
		panic(err)
	}
	capabilityRegistry, err := appconfig.BuildPaymentCapabilityRegistry(reg, alipayCfg, paypalCfg, wechatCfg)
	if err != nil {
		panic(err)
	}

	devSettle := g.Cfg().MustGet(ctx, "commerce.devSettle").Bool()
	// Prod-safety guard: devSettle backdoor must never reach a real Alipay endpoint.
	if devSettle {
		if alipayCfg.AppID != "" && !alipayCfg.Sandbox {
			log.Fatal("FATAL: commerce.devSettle=true but Alipay is configured for production (sandbox=false). " +
				"The devSettle backdoor must not be enabled against non-sandbox Alipay. Aborting.")
		}
		g.Log().Warning(ctx, "WARNING: commerce.devSettle=true — dev-only settle backdoor is active. "+
			"Never enable this in production.")
	}

	notifyURL := alipayCfg.NotifyURL
	returnURL := alipayCfg.ReturnURL
	siteResolver := appconfig.LoadSiteContext(ctx)
	currentDelivery := appconfig.BuildCurrentDeliveryResolver(ctx, siteResolver)
	if currentDelivery == nil {
		log.Fatal("FATAL: commerce.shopService.base_url is required. Commerce checkout must resolve authoritative product snapshots from shop service.")
	}

	db := dao.NewPG(g.DB())
	var webhookRuntime *webhooksetup.Runtime
	if g.Cfg().MustGet(ctx, "commerce.webhook.enabled").Bool() {
		masterKey, err := webhooksetup.DecodeMasterKey(
			g.Cfg().MustGet(ctx, "commerce.webhook.masterKey").String(),
		)
		if err != nil {
			panic(err)
		}
		webhookDB, err := postgresdb.OpenDefault(ctx)
		if err != nil {
			panic(err)
		}
		defer webhookDB.Close()
		workerID := "commerce-webhook-worker"
		if hostname, hostErr := os.Hostname(); hostErr == nil && hostname != "" {
			workerID += ":" + hostname
		}
		webhookRuntime, err = webhooksetup.New(ctx, webhooksetup.Options{
			DB: webhookDB, InstanceKey: "commerce:default",
			Definition: commercewebhook.Definition("default"),
			MasterKey:  masterKey, WorkerID: workerID,
			OnError: func(runErr error) {
				g.Log().Warning(ctx, "commerce webhook runner error", "error", runErr)
			},
		})
		if err != nil {
			panic(err)
		}
		workerContext, stopWorker := context.WithCancel(context.Background())
		defer stopWorker()
		go func() {
			if runErr := webhookRuntime.Runner.Run(workerContext); runErr != nil && !errors.Is(runErr, context.Canceled) {
				g.Log().Error(ctx, "commerce webhook runner stopped", "error", runErr)
			}
		}()
	}
	s := g.Server()
	var webhookPublisher commerceservice.TransactionalWebhookPublisher
	if webhookRuntime != nil {
		webhookPublisher = webhookRuntime.Hooks
	}
	server.Configure(s, server.Deps{
		Verifier:             verifier,
		DB:                   db,
		Registry:             reg,
		NotifyURL:            notifyURL,
		ReturnURL:            returnURL,
		DevSettle:            devSettle,
		Checkin:              appconfig.LoadCheckin(ctx),
		Delivery:             appconfig.LoadDelivery(ctx),
		Mailer:               appconfig.BuildDeliveryMailer(ctx),
		Asset:                appconfig.BuildAssetDeliveryClient(ctx),
		CurrentDelivery:      currentDelivery,
		SiteContext:          siteResolver,
		Capabilities:         capabilityRegistry,
		CapabilityScope:      appconfig.CapabilityScope(ctx),
		CapabilityProbeScope: appconfig.CapabilityProbeScope(ctx),
		CapabilityService:    appconfig.CapabilityServiceMetadata(),
		Webhooks:             webhookPublisher,
	})
	if handled, err := openapiexport.ExportIfRequested(s); handled {
		if err != nil {
			panic(err)
		}
		return
	}
	g.Log().Info(ctx, "commerce-service starting")
	s.Run()
}

func buildGatewayRegistry(alipayCfg appconfig.Alipay, paypalCfg appconfig.PayPal, wechatCfg appconfig.WeChat) (paykit.Registry, error) {
	reg := paykit.NewRegistry()
	providerHTTPClient := observability.HTTPClient(&http.Client{Timeout: 15 * time.Second})
	if alipayCfg.AppID != "" && alipayCfg.PrivateKey != "" {
		alipayGW, err := payalipay.NewProvider(payalipay.Config{
			AppID:           alipayCfg.AppID,
			PrivateKey:      alipayCfg.PrivateKey,
			AlipayPublicKey: alipayCfg.AlipayPublicKey,
			Sandbox:         alipayCfg.Sandbox,
			HTTPClient:      providerHTTPClient,
		})
		if err != nil {
			return nil, err
		}
		if err := reg.Register(alipayGW); err != nil {
			return nil, err
		}
	}
	if paypalCfg.ClientID != "" && paypalCfg.ClientSecret != "" {
		paypalGW, err := paypal.NewProvider(paypal.Config{
			ClientID:     paypalCfg.ClientID,
			ClientSecret: paypalCfg.ClientSecret,
			Sandbox:      paypalCfg.Sandbox,
			BaseURL:      paypalCfg.BaseURL,
			HTTPClient:   providerHTTPClient,
		})
		if err != nil {
			return nil, err
		}
		if err := reg.Register(paypalGW); err != nil {
			return nil, err
		}
	}
	if wechatCfg.MerchantID != "" && wechatCfg.MerchantCertSN != "" && wechatCfg.MerchantAPIv3Key != "" &&
		wechatCfg.PrivateKey != "" && wechatCfg.AppID != "" {
		wechatGW, err := wechat.NewProvider(wechat.Config{
			MerchantID:       wechatCfg.MerchantID,
			MerchantCertSN:   wechatCfg.MerchantCertSN,
			MerchantAPIv3Key: wechatCfg.MerchantAPIv3Key,
			PrivateKey:       wechatCfg.PrivateKey,
			AppID:            wechatCfg.AppID,
			NotifyURL:        wechatCfg.NotifyURL,
			HTTPClient:       providerHTTPClient,
		})
		if err != nil {
			return nil, err
		}
		if err := reg.Register(wechatGW); err != nil {
			return nil, err
		}
	}
	return reg, nil
}
