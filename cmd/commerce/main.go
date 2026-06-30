// Command commerce is the payment and entitlement service.
package main

import (
	"log"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gctx"

	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"platform/gokit/authjwt"
	"platform/services/commerce/internal/appconfig"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/gateway"
	"platform/services/commerce/internal/server"
)

func main() {
	ctx := gctx.New()

	jw := appconfig.LoadJWKS(ctx)
	verifier, err := authjwt.NewVerifier(authjwt.VerifierConfig{
		Keys:     authjwt.NewRemoteKeySource(jw.URL),
		Issuer:   jw.Issuer,
		Audience: jw.Audience,
	})
	if err != nil {
		panic(err)
	}

	// Build the Alipay provider from config.
	// When credentials are absent (local dev default config) the provider is
	// omitted from the registry; the service boots but CreateOrder will return
	// commerce.gateway_failed until real credentials are supplied.
	alipayCfg := appconfig.LoadAlipay(ctx)
	reg := gateway.Registry{}
	if alipayCfg.AppID != "" && alipayCfg.PrivateKey != "" {
		alipayGW, err := gateway.NewAlipayProvider(gateway.AlipayConfig{
			AppID:           alipayCfg.AppID,
			PrivateKey:      alipayCfg.PrivateKey,
			AlipayPublicKey: alipayCfg.AlipayPublicKey,
			Sandbox:         alipayCfg.Sandbox,
		})
		if err != nil {
			panic(err)
		}
		reg["alipay"] = alipayGW
	}
	paypalCfg := appconfig.LoadPayPal(ctx)
	if paypalCfg.ClientID != "" && paypalCfg.ClientSecret != "" {
		paypalGW, err := gateway.NewPayPalProvider(gateway.PayPalConfig{
			ClientID:     paypalCfg.ClientID,
			ClientSecret: paypalCfg.ClientSecret,
			Sandbox:      paypalCfg.Sandbox,
			BaseURL:      paypalCfg.BaseURL,
		})
		if err != nil {
			panic(err)
		}
		reg["paypal"] = paypalGW
	}
	wechatCfg := appconfig.LoadWeChat(ctx)
	if wechatCfg.MerchantID != "" && wechatCfg.MerchantCertSN != "" && wechatCfg.MerchantAPIv3Key != "" &&
		wechatCfg.PrivateKey != "" && wechatCfg.AppID != "" {
		wechatGW, err := gateway.NewWeChatProvider(gateway.WeChatConfig{
			MerchantID:       wechatCfg.MerchantID,
			MerchantCertSN:   wechatCfg.MerchantCertSN,
			MerchantAPIv3Key: wechatCfg.MerchantAPIv3Key,
			PrivateKey:       wechatCfg.PrivateKey,
			AppID:            wechatCfg.AppID,
			NotifyURL:        wechatCfg.NotifyURL,
		})
		if err != nil {
			panic(err)
		}
		reg["wechat"] = wechatGW
	}

	devSettle := g.Cfg().MustGet(ctx, "commerce.devSettle").Bool()
	if devSettle {
		if _, ok := reg["alipay"]; !ok {
			reg["alipay"] = gateway.NewAlipayStubProvider()
		}
		if _, ok := reg["wechat"]; !ok {
			reg["wechat"] = gateway.NewWeChatStubProvider()
		}
		if _, ok := reg["paypal"]; !ok {
			reg["paypal"] = gateway.NewPayPalStubProvider()
		}
	}

	// Prod-safety guard: devSettle backdoor must never reach a real Alipay endpoint.
	if devSettle {
		if alipayCfg.AppID != "" && !alipayCfg.Sandbox {
			// Non-sandbox Alipay config with devSettle enabled — refuse to start.
			log.Fatal("FATAL: commerce.devSettle=true but Alipay is configured for production (sandbox=false). " +
				"The devSettle backdoor must not be enabled against non-sandbox Alipay. Aborting.")
		}
		g.Log().Warning(ctx, "WARNING: commerce.devSettle=true — dev-only settle backdoor is active. "+
			"Never enable this in production.")
	}

	notifyURL := alipayCfg.NotifyURL
	returnURL := alipayCfg.ReturnURL

	db := dao.NewPG(g.DB())
	s := g.Server()
	server.Configure(s, server.Deps{
		Verifier:  verifier,
		DB:        db,
		Registry:  reg,
		NotifyURL: notifyURL,
		ReturnURL: returnURL,
		DevSettle: devSettle,
		Checkin:   appconfig.LoadCheckin(ctx),
		Delivery:  appconfig.LoadDelivery(ctx),
		Mailer:    appconfig.BuildDeliveryMailer(ctx),
		Asset:     appconfig.BuildAssetDeliveryClient(ctx),
	})
	g.Log().Info(ctx, "commerce-service starting")
	s.Run()
}
