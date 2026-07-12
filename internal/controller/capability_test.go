package controller

import (
	"context"
	"testing"

	"platform/gokit/authjwt"
	"platform/gokit/capability"
)

func TestCapabilityAuthorizationAcceptsAdminOrDedicatedScope(t *testing.T) {
	controller := NewCapability(nil, nil, capability.ServiceMetadata{}, "platform:capabilities:read")
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
			_, err := controller.authorize(ctx)
			if test.allowed && err != nil {
				t.Fatalf("authorize() error = %v", err)
			}
			if !test.allowed && err == nil {
				t.Fatal("authorize() unexpectedly allowed principal")
			}
		})
	}
}
