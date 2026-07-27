package runtime

import (
	"context"
	"net/http"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	goframehealth "github.com/yueli-official/foundation/go/goframe/health"
	"github.com/yueli-official/foundation/go/health"
	"github.com/yueli-official/foundation/go/problem"
)

type ReadinessCheck = health.Check

var notReady = problem.MustDescriptor(
	problem.MustKind("common.not_ready", http.StatusServiceUnavailable),
	"https://errors.yueli.dev/problems/common.not_ready",
)

func DatabaseReadiness(ctx context.Context) error {
	_, err := g.DB().GetValue(ctx, "SELECT 1")
	return err
}

func ReadinessHandler(checks map[string]ReadinessCheck) func(*ghttp.Request) {
	runner := health.MustRunner(checks, health.RunnerOptions{
		Timeout: 3 * time.Second,
		OnPanic: func(name string, _ any) {
			g.Log().Errorf(context.Background(), "readiness check %s panicked", name)
		},
	})
	handler, err := goframehealth.Handler(goframehealth.HandlerOptions{
		Runner:   runner,
		NotReady: notReady,
		TraceID: func(request *ghttp.Request) string {
			return request.Response.Header().Get("X-Trace-Id")
		},
	})
	if err != nil {
		panic(err)
	}
	return handler
}
