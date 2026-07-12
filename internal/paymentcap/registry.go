package paymentcap

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"platform/gokit/capability"
	"platform/paykit"
)

type Definition struct {
	Instance       string
	Adapter        string
	Mode           string
	Operations     []string
	RequiredConfig []capability.ConfigField
	Gateway        paykit.Provider
}

type MethodState struct {
	Provider string
	Enabled  bool
}

type providerState struct {
	definition Definition
	health     capability.Health
	checkedAt  *time.Time
	probeMu    sync.Mutex
}

type Registry struct {
	mu          sync.Mutex
	providers   map[string]*providerState
	service     capability.ServiceMetadata
	methods     []MethodState
	fingerprint string
	snapshot    *capability.Snapshot
	generatedAt time.Time
}

func New(definitions ...Definition) (*Registry, error) {
	registry := &Registry{providers: map[string]*providerState{}}
	for _, definition := range definitions {
		definition.Instance = strings.TrimSpace(definition.Instance)
		definition.Adapter = strings.TrimSpace(definition.Adapter)
		if definition.Instance == "" || definition.Adapter == "" {
			return nil, fmt.Errorf("commerce payment provider instance and adapter are required")
		}
		if _, exists := registry.providers[definition.Instance]; exists {
			return nil, fmt.Errorf("duplicate commerce payment provider %q", definition.Instance)
		}
		registry.providers[definition.Instance] = &providerState{definition: definition, health: capability.HealthUnknown}
	}
	return registry, nil
}

func (registry *Registry) Snapshot(service capability.ServiceMetadata, methods []MethodState, generatedAt time.Time) (*capability.Snapshot, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	fingerprint := methodFingerprint(methods)
	if registry.snapshot != nil && registry.service == service && registry.fingerprint == fingerprint {
		return registry.snapshot, nil
	}
	registry.service = service
	registry.methods = append([]MethodState{}, methods...)
	registry.fingerprint = fingerprint
	return registry.rebuildLocked(generatedAt)
}

func (registry *Registry) CheckHealth(ctx context.Context, key string) error {
	registry.mu.Lock()
	provider := registry.providers[strings.TrimSpace(key)]
	registry.mu.Unlock()
	if provider == nil {
		return fmt.Errorf("commerce payment provider %q not found", key)
	}
	provider.probeMu.Lock()
	defer provider.probeMu.Unlock()
	for _, field := range provider.definition.RequiredConfig {
		if field.State != capability.ConfigStatePresent {
			err := fmt.Errorf("commerce payment provider %q configuration is incomplete", key)
			registry.recordHealth(provider, capability.HealthUnhealthy)
			return err
		}
	}
	checker, ok := provider.definition.Gateway.(paykit.HealthChecker)
	var err error
	if !ok {
		err = fmt.Errorf("commerce payment provider %q has no safe health checker", key)
	} else {
		err = checker.CheckHealth(ctx)
	}
	if err != nil {
		registry.recordHealth(provider, capability.HealthUnhealthy)
	} else {
		registry.recordHealth(provider, capability.HealthHealthy)
	}
	return err
}

func (registry *Registry) rebuildLocked(generatedAt time.Time) (*capability.Snapshot, error) {
	generatedAt = generatedAt.UTC()
	if !registry.generatedAt.IsZero() && !generatedAt.After(registry.generatedAt) {
		generatedAt = registry.generatedAt.Add(time.Nanosecond)
	}
	enabled := map[string]bool{}
	for _, method := range registry.methods {
		enabled[strings.TrimSpace(method.Provider)] = method.Enabled
	}
	keys := make([]string, 0, len(registry.providers))
	for key := range registry.providers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	providers := make([]capability.Provider, 0, len(keys))
	var (
		anyConfigured bool
		anyEnabled    bool
		anyHealthy    bool
		anyUnhealthy  bool
		operations    []string
	)
	for _, key := range keys {
		state := registry.providers[key]
		enablement := capability.EnablementDisabled
		if enabled[state.definition.Adapter] {
			enablement = capability.EnablementEnabled
			anyEnabled = true
		}
		configuration := configurationFrom(state.definition.RequiredConfig)
		if configuration == capability.ConfigurationComplete {
			anyConfigured = true
		}
		if enablement == capability.EnablementEnabled && configuration == capability.ConfigurationComplete {
			anyHealthy = anyHealthy || state.health == capability.HealthHealthy
			anyUnhealthy = anyUnhealthy || state.health == capability.HealthUnhealthy
		}
		operations = append(operations, state.definition.Operations...)
		providers = append(providers, capability.Provider{
			Key: key, Adapter: state.definition.Adapter, Registered: state.definition.Gateway != nil, CapabilityKeys: []string{"commerce.payment"},
			Configuration: configuration, Enablement: enablement, Health: state.health, Mode: state.definition.Mode,
			Operations: state.definition.Operations, RequiredConfig: state.definition.RequiredConfig, LastCheckedAt: state.checkedAt,
			Links: []capability.Link{{Rel: "health-check", Href: "/api/v1/admin/providers/" + key + "/health-check"}},
		})
	}
	configuration := capability.ConfigurationMissing
	if anyConfigured {
		configuration = capability.ConfigurationComplete
	}
	enablement := capability.EnablementDisabled
	if anyEnabled {
		enablement = capability.EnablementEnabled
	}
	health := capability.HealthUnknown
	if anyHealthy {
		health = capability.HealthHealthy
	} else if anyUnhealthy {
		health = capability.HealthUnhealthy
	}
	snapshot, err := capability.NewSnapshot(capability.Manifest{
		Service: registry.service, GeneratedAt: generatedAt, Redaction: capability.RedactionMetadata{Policy: "presence-only", Version: "1"},
		Capabilities: []capability.Capability{{
			Key: "commerce.payment", ContractVersion: "1.0", Support: capability.SupportSupported,
			Configuration: configuration, Enablement: enablement, Health: health, Operations: uniqueSorted(operations),
			Links: []capability.Link{{Rel: "payment-methods", Href: "/api/v1/admin/commerce/payment-methods"}},
		}},
		Providers: providers,
		Links:     []capability.Link{{Rel: "health", Href: "/healthz"}, {Rel: "payment-methods", Href: "/api/v1/admin/commerce/payment-methods"}, {Rel: "ready", Href: "/readyz"}},
	})
	if err != nil {
		return nil, err
	}
	registry.snapshot = snapshot
	registry.generatedAt = generatedAt
	return snapshot, nil
}

func (registry *Registry) recordHealth(provider *providerState, health capability.Health) {
	now := time.Now().UTC()
	registry.mu.Lock()
	provider.health = health
	provider.checkedAt = &now
	registry.snapshot = nil
	if registry.service.Name != "" {
		_, _ = registry.rebuildLocked(now)
	}
	registry.mu.Unlock()
}

func configurationFrom(fields []capability.ConfigField) capability.Configuration {
	if len(fields) == 0 {
		return capability.ConfigurationComplete
	}
	present := 0
	for _, field := range fields {
		if field.State == capability.ConfigStatePresent {
			present++
		}
	}
	if present == 0 {
		return capability.ConfigurationMissing
	}
	if present == len(fields) {
		return capability.ConfigurationComplete
	}
	return capability.ConfigurationPartial
}

func methodFingerprint(methods []MethodState) string {
	values := make([]string, 0, len(methods))
	for _, method := range methods {
		values = append(values, fmt.Sprintf("%s=%t", strings.TrimSpace(method.Provider), method.Enabled))
	}
	sort.Strings(values)
	return strings.Join(values, ";")
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
