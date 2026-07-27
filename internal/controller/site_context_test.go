package controller

import (
	"context"
	"testing"

	foundationauth "github.com/yueli-official/foundation/go/auth"
	v1 "github.com/yueli-official/commerce/api/v1"
	"github.com/yueli-official/commerce/internal/commerceerr"
	"github.com/yueli-official/commerce/internal/sitecontext"
)

func TestCheckoutItemsForceTrustedSiteForEveryItem(t *testing.T) {
	resolver := sitecontext.New([]sitecontext.Context{{SiteKey: "shop-main"}})
	ctx := sitecontext.With(context.Background(), sitecontext.Context{SiteKey: "shop-main"})

	items, err := checkoutItems(ctx, resolver, []v1.CheckoutItemReq{
		{SiteKey: "shop-ui", ExternalID: "product-1"},
		{SiteKey: "", ExternalID: "product-2"},
	})
	if err != nil {
		t.Fatalf("checkoutItems() error = %v", err)
	}
	for i, item := range items {
		if item.SiteKey != "shop-main" {
			t.Fatalf("items[%d].SiteKey = %q, want shop-main", i, item.SiteKey)
		}
	}
}

func TestRequireAdminAcceptsOnlyTrustedSiteOrLegacyAdmin(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		wantErr bool
	}{
		{
			name: "trusted site assertion context",
			ctx: sitecontext.With(
				context.Background(),
				sitecontext.Context{SiteKey: "shop-main"},
			),
		},
		{
			name: "legacy commerce administrator",
			ctx: foundationauth.NewContext(
				context.Background(),
				&foundationauth.Principal{Subject: "legacy-admin", Roles: []string{"admin"}},
			),
		},
		{
			name: "ordinary browser principal",
			ctx: foundationauth.NewContext(
				context.Background(),
				&foundationauth.Principal{Subject: "browser-user"},
			),
			wantErr: true,
		},
		{
			name:    "anonymous request",
			ctx:     context.Background(),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := requireAdmin(test.ctx)
			if !test.wantErr {
				if err != nil {
					t.Fatalf("requireAdmin() error = %v", err)
				}
				return
			}
			value, ok := commerceerr.Resolve(err)
			if !ok || value.Code != commerceerr.CodeForbidden {
				t.Fatalf("requireAdmin() error = %v, want %s", err, commerceerr.CodeForbidden)
			}
		})
	}
}
