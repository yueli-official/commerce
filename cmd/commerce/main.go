// Command commerce is the payment and entitlement service.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"maps"
	"net/http"
	"os"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gctx"
	"github.com/yueli-official/foundation/go/work"
	workpostgres "github.com/yueli-official/foundation/go/work/postgres"

	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"github.com/yueli-official/commerce/paykit"
	payalipay "github.com/yueli-official/commerce/paykit/providers/alipay"
	paydev "github.com/yueli-official/commerce/paykit/providers/dev"
	paypal "github.com/yueli-official/commerce/paykit/providers/paypal"
	wechat "github.com/yueli-official/commerce/paykit/providers/wechat"
	"platform/services/commerce/internal/appconfig"
	"platform/services/commerce/internal/commerceaudit"
	"platform/services/commerce/internal/commercewebhook"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/deliveryrecovery"
	"platform/services/commerce/internal/paymentreconcile"
	commerceruntime "platform/services/commerce/internal/runtime"
	"platform/services/commerce/internal/server"
	commerceservice "platform/services/commerce/internal/service"
)

func main() {
	ctx := gctx.New()
	shutdown, err := commerceruntime.StartTelemetry(ctx, "commerce-api")
	if err != nil {
		panic(err)
	}
	defer commerceruntime.ShutdownTelemetry(shutdown)

	jw := appconfig.LoadJWKS(ctx)
	verifier, err := commerceruntime.NewRemoteVerifier(commerceruntime.RemoteVerifierConfig{
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
	devSettle := g.Cfg().MustGet(ctx, "commerce.devSettle").Bool()
	reg, err := buildGatewayRegistry(devSettle, alipayCfg, paypalCfg, wechatCfg)
	if err != nil {
		panic(err)
	}
	capabilityRegistry, err := appconfig.BuildPaymentCapabilityRegistry(reg, alipayCfg, paypalCfg, wechatCfg)
	if err != nil {
		panic(err)
	}

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
	exportingOpenAPI := commerceruntime.OpenAPIRequested()
	var webhookRuntime *commerceruntime.WebhookRuntime
	if g.Cfg().MustGet(ctx, "commerce.webhook.enabled").Bool() {
		masterKey, err := commerceruntime.DecodeWebhookMasterKey(
			g.Cfg().MustGet(ctx, "commerce.webhook.masterKey").String(),
		)
		if err != nil {
			panic(err)
		}
		webhookDB, err := commerceruntime.OpenDefaultPostgres(ctx)
		if err != nil {
			panic(err)
		}
		defer webhookDB.Close()
		workerID := "commerce-webhook-worker"
		if hostname, hostErr := os.Hostname(); hostErr == nil && hostname != "" {
			workerID += ":" + hostname
		}
		webhookRuntime, err = commerceruntime.NewWebhook(ctx, commerceruntime.WebhookOptions{
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
	deps := server.Deps{
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
	}
	svc := server.NewService(deps)
	deps.Service = svc

	var paymentWorker *work.Runner
	var foundationDB *sql.DB
	if !exportingOpenAPI {
		foundationDB, err = commerceruntime.OpenDefaultPostgres(ctx)
		if err != nil {
			panic(err)
		}
		defer foundationDB.Close()
		auditJournal, auditErr := commerceaudit.New(
			ctx, foundationDB,
			g.Cfg().MustGet(ctx, "commerce.instanceKey", "default").String(),
		)
		if auditErr != nil {
			panic(auditErr)
		}
		db.UseAudit(auditJournal)
	}
	if !exportingOpenAPI && g.Cfg().MustGet(ctx, "commerce.worker.enabled", true).Bool() {
		definition := deliveryrecovery.ExtendWorkDefinition(
			paymentreconcile.WorkDefinition(),
			paymentreconcile.WorkQueueReconciliation,
		)
		catalog, err := work.Compile(definition)
		if err != nil {
			panic(err)
		}
		adapter, err := workpostgres.New(ctx, catalog, workpostgres.Options{
			DB: foundationDB, InstanceKey: "commerce:payment-reconciliation:v1",
		})
		if err != nil {
			panic(err)
		}
		workerID := g.Cfg().MustGet(ctx, "commerce.worker.id").String()
		if workerID == "" {
			if hostname, hostErr := os.Hostname(); hostErr == nil && hostname != "" {
				workerID = "commerce-" + hostname
			} else {
				workerID = "commerce-worker"
			}
		}
		handlers := paymentreconcile.WorkHandlers(
			db, adapter, paymentreconcile.New(svc, reg), time.Now,
		)
		var assetRevoker deliveryrecovery.Revoker
		if configured, ok := deps.Asset.(deliveryrecovery.Revoker); ok {
			assetRevoker = configured
		}
		maps.Copy(handlers, deliveryrecovery.WorkHandlers(
			db, adapter, assetRevoker, appconfig.BuildRecoveryNotifier(ctx), time.Now,
		))
		paymentWorker, err = work.NewRunner(
			catalog,
			adapter,
			handlers,
			work.RunnerOptions{
				WorkerID: workerID,
				PollInterval: g.Cfg().MustGet(
					ctx, "commerce.worker.pollInterval", "2s",
				).Duration(),
				OnError: func(runErr error) {
					g.Log().Warning(ctx, "commerce payment worker error", "error", runErr)
				},
			},
		)
		if err != nil {
			panic(err)
		}
	}
	server.Configure(s, deps)
	if handled, err := commerceruntime.ExportOpenAPIIfRequested(s); handled {
		if err != nil {
			panic(err)
		}
		return
	}
	if paymentWorker != nil {
		workerContext, stopWorker := context.WithCancel(ctx)
		defer stopWorker()
		go func() {
			if runErr := paymentWorker.Run(workerContext); runErr != nil &&
				!errors.Is(runErr, context.Canceled) {
				g.Log().Error(ctx, "commerce payment worker stopped", "error", runErr)
			}
		}()
	}
	g.Log().Info(ctx, "commerce-service starting")
	s.Run()
}

func buildGatewayRegistry(devSettle bool, alipayCfg appconfig.Alipay, paypalCfg appconfig.PayPal, wechatCfg appconfig.WeChat) (paykit.Registry, error) {
	reg := paykit.NewRegistry()
	if devSettle {
		if err := reg.Register(paydev.NewProvider()); err != nil {
			return nil, err
		}
	}
	providerHTTPClient := commerceruntime.TelemetryHTTPClient(&http.Client{Timeout: 15 * time.Second})
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
			WebhookID:    paypalCfg.WebhookID,
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
