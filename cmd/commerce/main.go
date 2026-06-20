// Command commerce is the payment and entitlement service (Task 1 shell).
package main

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gctx"

	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"platform/gokit/authjwt"
	"platform/services/commerce/internal/appconfig"
	"platform/services/commerce/internal/dao"
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

	db := dao.NewPG(g.DB())
	s := g.Server()
	server.Configure(s, server.Deps{Verifier: verifier, DB: db})
	g.Log().Info(ctx, "commerce-service starting")
	s.Run()
}
