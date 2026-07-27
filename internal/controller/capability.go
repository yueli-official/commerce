package controller

import (
	"context"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	foundationauth "github.com/yueli-official/foundation/go/auth"
	"github.com/yueli-official/foundation/go/capability"
	"github.com/yueli-official/foundation/go/goframe/ratelimit"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/paymentcap"
	"platform/services/commerce/internal/service"
)

type Capability struct {
	service       *service.Service
	registry      *paymentcap.Registry
	metadata      capability.ServiceMetadata
	readScope     string
	probeScope    string
	healthLimiter *ratelimit.Limiter
}

func NewCapability(service *service.Service, registry *paymentcap.Registry, metadata capability.ServiceMetadata, readScope, probeScope string) *Capability {
	if strings.TrimSpace(readScope) == "" {
		readScope = "platform:capabilities:read"
	}
	if strings.TrimSpace(probeScope) == "" {
		probeScope = "platform:capabilities:probe"
	}
	return &Capability{
		service: service, registry: registry, metadata: metadata,
		readScope: strings.TrimSpace(readScope), probeScope: strings.TrimSpace(probeScope),
		healthLimiter: ratelimit.MustNew(ratelimit.Policy{Limit: 5, Window: time.Minute}),
	}
}

func (controller *Capability) Capabilities(ctx context.Context, _ *v1.AdminCapabilitiesReq) (*v1.AdminCapabilitiesRes, error) {
	snapshot, err := controller.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &v1.AdminCapabilitiesRes{Manifest: snapshot.Manifest()}, nil
}

func (controller *Capability) Capability(ctx context.Context, req *v1.AdminCapabilityReq) (*v1.AdminCapabilityRes, error) {
	snapshot, err := controller.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	item, ok := snapshot.Capability(req.Key)
	if !ok {
		return nil, commerceerr.CapabilityNotFound(req.Key)
	}
	return &v1.AdminCapabilityRes{Capability: item}, nil
}

func (controller *Capability) Providers(ctx context.Context, _ *v1.AdminProvidersReq) (*v1.AdminProvidersRes, error) {
	snapshot, err := controller.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &v1.AdminProvidersRes{Items: snapshot.ListProviders()}, nil
}

func (controller *Capability) Provider(ctx context.Context, req *v1.AdminProviderReq) (*v1.AdminProviderRes, error) {
	snapshot, err := controller.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	item, ok := snapshot.Provider(req.Key)
	if !ok {
		return nil, commerceerr.ProviderNotFound(req.Key)
	}
	return &v1.AdminProviderRes{Provider: item}, nil
}

func (controller *Capability) ProviderHealthCheck(ctx context.Context, req *v1.AdminProviderHealthCheckReq) (*v1.AdminProviderHealthCheckRes, error) {
	principal, err := controller.authorizeProbe(ctx)
	if err != nil {
		return nil, err
	}
	actor := principal.ActorKey()
	if decision := controller.healthLimiter.Evaluate(actor + "|" + strings.TrimSpace(req.Key)); !decision.Allowed {
		return nil, commerceerr.HealthCheckRateLimited(req.Key)
	}
	snapshot, err := controller.snapshotAuthorized(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := snapshot.Provider(req.Key); !ok {
		return nil, commerceerr.ProviderNotFound(req.Key)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	startedAt := time.Now()
	probeErr := controller.registry.CheckHealth(probeCtx, req.Key)
	g.Log().Info(ctx, "commerce payment provider health check", "provider", strings.TrimSpace(req.Key), "actor", actor, "clientId", principal.ClientID, "durationMs", time.Since(startedAt).Milliseconds(), "succeeded", probeErr == nil)
	snapshot, err = controller.snapshotAuthorized(ctx)
	if err != nil {
		return nil, err
	}
	item, _ := snapshot.Provider(req.Key)
	return &v1.AdminProviderHealthCheckRes{Provider: item}, nil
}

func (controller *Capability) snapshot(ctx context.Context) (*capability.Snapshot, error) {
	if _, err := controller.authorizeRead(ctx); err != nil {
		return nil, err
	}
	return controller.snapshotAuthorized(ctx)
}

func (controller *Capability) snapshotAuthorized(ctx context.Context) (*capability.Snapshot, error) {
	methods, err := controller.service.PaymentMethods(ctx)
	if err != nil {
		return nil, err
	}
	states := make([]paymentcap.MethodState, 0, len(methods))
	for _, method := range methods {
		states = append(states, paymentcap.MethodState{Provider: method.Provider, Enabled: method.Enabled})
	}
	return controller.registry.Snapshot(controller.metadata, states, time.Now())
}

func (controller *Capability) authorizeRead(ctx context.Context) (*foundationauth.Principal, error) {
	principal, ok := foundationauth.FromContext(ctx)
	if !ok || principal == nil || (!principal.HasRole("admin") && !principal.HasScope(controller.readScope)) {
		return nil, commerceerr.Forbidden()
	}
	return principal, nil
}

func (controller *Capability) authorizeProbe(ctx context.Context) (*foundationauth.Principal, error) {
	principal, ok := foundationauth.FromContext(ctx)
	if !ok || principal == nil || (!principal.HasRole("admin") && !principal.HasScope(controller.probeScope)) {
		return nil, commerceerr.Forbidden()
	}
	return principal, nil
}
