package sitecontext

import (
	"context"
	"errors"
	"testing"
	"time"

	foundationauth "github.com/yueli-official/foundation/go/auth"
)

func testResolver() *Resolver {
	return New([]Context{{
		SiteKey:         "shop-main",
		ClientIDs:       []string{"shop-main-web"},
		AssertionSecret: "test-commerce-site-secret-0123456789",
		ShopBaseURL:     "http://127.0.0.1:8088",
	}})
}

func TestResolverUsesVerifiedOAuthClient(t *testing.T) {
	resolver := testResolver()
	got, ok := resolver.ResolvePrincipal(&foundationauth.Principal{ClientID: "shop-main-web"})
	if !ok || got.SiteKey != "shop-main" {
		t.Fatalf("ResolvePrincipal() = %#v, %v", got, ok)
	}
}

func TestResolverRejectsClientMappedToMultipleSites(t *testing.T) {
	resolver := New([]Context{
		{SiteKey: "shop-main", ClientIDs: []string{"shared-web"}},
		{SiteKey: "shop-ui", ClientIDs: []string{"shared-web"}},
	})
	if _, ok := resolver.ResolvePrincipal(&foundationauth.Principal{ClientID: "shared-web"}); ok {
		t.Fatal("ambiguous OAuth client must not resolve to a site")
	}
}

func TestResolverVerifiesSiteAssertion(t *testing.T) {
	resolver := testResolver()
	now := time.Unix(1_750_000_000, 0)
	timestamp := "1750000000"
	signature := SignAssertion("test-commerce-site-secret-0123456789", "shop-main", timestamp)

	got, err := resolver.VerifyAssertion("shop-main", timestamp, signature, now)
	if err != nil || got.SiteKey != "shop-main" {
		t.Fatalf("VerifyAssertion() = %#v, %v", got, err)
	}
}

func TestResolverRejectsTamperedAndExpiredAssertions(t *testing.T) {
	resolver := testResolver()
	now := time.Unix(1_750_000_000, 0)
	timestamp := "1750000000"
	signature := SignAssertion("test-commerce-site-secret-0123456789", "shop-main", timestamp)

	for _, tc := range []struct {
		name      string
		siteKey   string
		timestamp string
		signature string
		now       time.Time
	}{
		{name: "site", siteKey: "shop-ui", timestamp: timestamp, signature: signature, now: now},
		{name: "signature", siteKey: "shop-main", timestamp: timestamp, signature: signature + "00", now: now},
		{name: "expired", siteKey: "shop-main", timestamp: timestamp, signature: signature, now: now.Add(6 * time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolver.VerifyAssertion(tc.siteKey, tc.timestamp, tc.signature, tc.now); !errors.Is(err, ErrInvalidAssertion) {
				t.Fatalf("VerifyAssertion() error = %v, want ErrInvalidAssertion", err)
			}
		})
	}
}

func TestRequireSiteForcesTrustedContext(t *testing.T) {
	resolver := testResolver()
	ctx := With(context.Background(), Context{SiteKey: "shop-main"})

	got, err := resolver.RequireSite(ctx, "shop-ui")
	if err != nil || got != "shop-main" {
		t.Fatalf("RequireSite() = %q, %v", got, err)
	}
}

func TestRequireSiteRejectsUnsignedManagedSiteButKeepsLegacyFallback(t *testing.T) {
	resolver := testResolver()
	if _, err := resolver.RequireSite(context.Background(), "shop-main"); !errors.Is(err, ErrTrustedContextRequired) {
		t.Fatalf("RequireSite(managed) error = %v, want ErrTrustedContextRequired", err)
	}
	if got, err := resolver.RequireSite(context.Background(), "legacy-product"); err != nil || got != "legacy-product" {
		t.Fatalf("RequireSite(legacy) = %q, %v", got, err)
	}
}

func TestRequireSiteStrictModeRejectsEveryUntrustedConsumer(t *testing.T) {
	resolver := NewWithRequired([]Context{{SiteKey: "shop-main"}}, true)
	if _, err := resolver.RequireSite(context.Background(), "legacy-product"); !errors.Is(err, ErrTrustedContextRequired) {
		t.Fatalf("RequireSite(strict legacy) error = %v, want ErrTrustedContextRequired", err)
	}
}
