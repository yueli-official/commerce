package runtime

import (
	"net/http"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
	foundationauth "github.com/yueli-official/foundation/go/auth"
	goframeauth "github.com/yueli-official/foundation/go/goframe/auth"
	foundationhttp "github.com/yueli-official/foundation/go/goframe/http"
	"github.com/yueli-official/foundation/go/jwks"
	"github.com/yueli-official/foundation/go/problem"
)

const (
	traceHeader             = "X-Trace-Id"
	unauthorizedProblemType = "https://yueli.dev/problems/common.unauthorized"
)

var unauthorizedKind = problem.MustKind("common.unauthorized", http.StatusUnauthorized)

type RemoteVerifierConfig struct {
	JWKSURL           string
	Issuer            string
	Audience          string
	AllowLoopbackHTTP bool
	Clock             func() time.Time
	Transport         jwks.RemoteOptions
}

type StaticVerifierConfig struct {
	Keys     jose.JSONWebKeySet
	Issuer   string
	Audience string
	Clock    func() time.Time
}

func NewRemoteVerifier(config RemoteVerifierConfig) (*foundationauth.Verifier, error) {
	transport := config.Transport
	transport.AllowLoopbackHTTP = transport.AllowLoopbackHTTP || config.AllowLoopbackHTTP
	transport.Client = TelemetryHTTPClient(transport.Client)
	keys, err := jwks.NewRemoteSource(config.JWKSURL, transport)
	if err != nil {
		return nil, err
	}
	return foundationauth.NewVerifier(foundationauth.Config{
		Keys: keys, Issuer: config.Issuer, Audiences: optionalAudience(config.Audience), Clock: config.Clock,
	})
}

func NewStaticVerifier(config StaticVerifierConfig) (*foundationauth.Verifier, error) {
	keys, err := jwks.NewStaticSource(config.Keys)
	if err != nil {
		return nil, err
	}
	return foundationauth.NewVerifier(foundationauth.Config{
		Keys: keys, Issuer: config.Issuer, Audiences: optionalAudience(config.Audience), Clock: config.Clock,
	})
}

func RequiredAuth(verifier goframeauth.TokenVerifier) func(*ghttp.Request) {
	return mustAuthMiddleware(verifier).Required
}

func OptionalAuth(verifier goframeauth.TokenVerifier) func(*ghttp.Request) {
	return mustAuthMiddleware(verifier).Optional
}

func mustAuthMiddleware(verifier goframeauth.TokenVerifier) *goframeauth.Middleware {
	writer := foundationhttp.MustWriter(foundationhttp.WriterOptions{TraceHeader: traceHeader})
	middleware, err := goframeauth.NewMiddleware(goframeauth.Options{
		Verifier:         verifier,
		Writer:           &writer,
		UnauthorizedKind: unauthorizedKind,
		UnauthorizedType: unauthorizedProblemType,
		TraceID: func(request *ghttp.Request) string {
			traceID := request.Response.Header().Get(traceHeader)
			if traceID == "" {
				traceID = request.Header.Get(traceHeader)
			}
			if traceID == "" {
				traceID = guid.S()
			}
			return traceID
		},
	})
	if err != nil {
		panic(err)
	}
	return middleware
}

func optionalAudience(audience string) []string {
	if audience == "" {
		return nil
	}
	return []string{audience}
}
