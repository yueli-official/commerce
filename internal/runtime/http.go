package runtime

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"
	goframeapi "github.com/yueli-official/foundation/go/goframe/api"
	"github.com/yueli-official/foundation/go/goframe/ratelimit"
	"github.com/yueli-official/commerce/internal/commerceerr"
)

const defaultRateLimitPerMinute = 600

func MustAPIMiddleware(limiter *ratelimit.Limiter) *goframeapi.Middleware {
	options := goframeapi.Options{
		TraceHeader: traceHeader,
		RateLimited: commerceerr.DescriptorRateLimited,
		Validation:  commerceerr.DescriptorValidation,
		Internal:    commerceerr.DescriptorInternal,
	}
	if limiter != nil {
		options.Limiter = limiter
		options.ClientKey = goframeapi.ForwardedClientIPKey
	}
	middleware, err := goframeapi.New(options)
	if err != nil {
		panic(err)
	}
	return middleware
}

func MustRateLimiterFromEnvironment() *ratelimit.Limiter {
	limit := defaultRateLimitPerMinute
	if raw := os.Getenv("PLATFORM_RATE_LIMIT_PER_MINUTE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			panic(fmt.Errorf("PLATFORM_RATE_LIMIT_PER_MINUTE must be a non-negative integer"))
		}
		limit = parsed
	}
	return ratelimit.MustNew(ratelimit.Policy{Limit: limit, Window: time.Minute})
}

func TraceRouteMiddleware(request *ghttp.Request) {
	goframeapi.TraceRoute(request)
}
