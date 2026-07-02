package paykit

import (
	"fmt"
	"strings"
)

type Registry map[string]Provider

func NewRegistry() Registry {
	return Registry{}
}

func (r Registry) Register(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("payment provider is nil")
	}
	name := NormalizeProvider(provider.Name())
	if name == "" {
		return fmt.Errorf("payment provider name is required")
	}
	if _, exists := r[name]; exists {
		return fmt.Errorf("payment provider %q already registered", name)
	}
	r[name] = provider
	return nil
}

func (r Registry) Get(name string) (Provider, bool) {
	provider, ok := r[NormalizeProvider(name)]
	return provider, ok
}

func NormalizeProvider(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
