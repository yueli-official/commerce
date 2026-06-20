// Command commerce is the payment and entitlement service.
package main

import (
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

	devSettle := g.Cfg().MustGet(ctx, "commerce.devSettle").Bool()
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
	})
	g.Log().Info(ctx, "commerce-service starting")
	s.Run()
}
