package controller

import (
	"context"
	"testing"

	"platform/gokit/authjwt"
	"platform/gokit/capability"
)

func TestCapabilityReadAuthorizationAcceptsAdminOrReadScope(t *testing.T) {
	controller := NewCapability(nil, nil, capability.ServiceMetadata{}, "platform:capabilities:read", "platform:capabilities:probe")
	for _, test := range []struct {
		name      string
		principal *authjwt.Principal
		allowed   bool
	}{
		{name: "admin", principal: &authjwt.Principal{Roles: []string{"admin"}}, allowed: true},
		{name: "service scope", principal: &authjwt.Principal{Scopes: []string{"platform:capabilities:read"}}, allowed: true},
		{name: "unprivileged", principal: &authjwt.Principal{Scopes: []string{"commerce:read"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := authjwt.WithPrincipal(context.Background(), test.principal)
			_, err := controller.authorizeRead(ctx)
			if test.allowed && err != nil {
				t.Fatalf("authorize() error = %v", err)
			}
			if !test.allowed && err == nil {
				t.Fatal("authorize() unexpectedly allowed principal")
			}
		})
	}
}

func TestCapabilityProbeAuthorizationRejectsReadOnlyScope(t *testing.T) {
	controller := NewCapability(nil, nil, capability.ServiceMetadata{}, "platform:capabilities:read", "platform:capabilities:probe")
	for _, test := range []struct {
		name      string
		principal *authjwt.Principal
		allowed   bool
	}{
		{name: "admin", principal: &authjwt.Principal{Roles: []string{"admin"}}, allowed: true},
		{name: "probe scope", principal: &authjwt.Principal{Scopes: []string{"platform:capabilities:probe"}}, allowed: true},
		{name: "read only", principal: &authjwt.Principal{Scopes: []string{"platform:capabilities:read"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := authjwt.WithPrincipal(context.Background(), test.principal)
			_, err := controller.authorizeProbe(ctx)
			if test.allowed && err != nil {
				t.Fatalf("authorizeProbe() error = %v", err)
			}
			if !test.allowed && err == nil {
				t.Fatal("authorizeProbe() unexpectedly allowed principal")
			}
		})
	}
}
