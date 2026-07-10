// Package sitecontext derives a trusted commerce site from a verified OAuth
// client or a short-lived assertion emitted by a site's server-side BFF.
package sitecontext

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"platform/gokit/authjwt"
)

var (
	ErrInvalidAssertion       = errors.New("commerce site context: invalid assertion")
	ErrTrustedContextRequired = errors.New("commerce site context: trusted context required")
)

const defaultMaxSkew = 5 * time.Minute

type Context struct {
	SiteKey         string
	ClientIDs       []string
	AssertionSecret string
	ShopBaseURL     string
}

type Resolver struct {
	bySite     map[string]Context
	byClientID map[string]Context
	maxSkew    time.Duration
	required   bool
}

func New(contexts []Context) *Resolver {
	return NewWithRequired(contexts, false)
}

func NewWithRequired(contexts []Context, required bool) *Resolver {
	resolver := &Resolver{
		bySite:     make(map[string]Context, len(contexts)),
		byClientID: map[string]Context{},
		maxSkew:    defaultMaxSkew,
		required:   required,
	}
	ambiguousClients := map[string]bool{}
	for _, item := range contexts {
		item.SiteKey = normalize(item.SiteKey)
		item.AssertionSecret = strings.TrimSpace(item.AssertionSecret)
		item.ShopBaseURL = strings.TrimRight(strings.TrimSpace(item.ShopBaseURL), "/")
		if item.SiteKey == "" {
			continue
		}
		clients := make([]string, 0, len(item.ClientIDs))
		for _, clientID := range item.ClientIDs {
			clientID = strings.TrimSpace(clientID)
			if clientID == "" {
				continue
			}
			clients = append(clients, clientID)
		}
		item.ClientIDs = clients
		resolver.bySite[item.SiteKey] = item
		for _, clientID := range clients {
			if ambiguousClients[clientID] {
				continue
			}
			if existing, ok := resolver.byClientID[clientID]; ok && existing.SiteKey != item.SiteKey {
				delete(resolver.byClientID, clientID)
				ambiguousClients[clientID] = true
				continue
			}
			resolver.byClientID[clientID] = item
		}
	}
	return resolver
}

func (r *Resolver) ResolvePrincipal(principal *authjwt.Principal) (Context, bool) {
	if r == nil || principal == nil {
		return Context{}, false
	}
	clientID := strings.TrimSpace(principal.ClientID)
	if clientID == "" {
		clientID, _ = principal.Claims["azp"].(string)
	}
	if item, ok := r.byClientID[clientID]; ok {
		return item, true
	}
	var resolved Context
	for _, audience := range principal.Audience {
		item, ok := r.byClientID[strings.TrimSpace(audience)]
		if !ok {
			continue
		}
		if resolved.SiteKey != "" && resolved.SiteKey != item.SiteKey {
			return Context{}, false
		}
		resolved = item
	}
	return resolved, resolved.SiteKey != ""
}

func (r *Resolver) VerifyAssertion(siteKey, timestamp, signature string, now time.Time) (Context, error) {
	if r == nil {
		return Context{}, ErrInvalidAssertion
	}
	item, ok := r.bySite[normalize(siteKey)]
	if !ok || len(item.AssertionSecret) < 32 {
		return Context{}, ErrInvalidAssertion
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return Context{}, ErrInvalidAssertion
	}
	assertedAt := time.Unix(seconds, 0)
	delta := now.Sub(assertedAt)
	if delta < -r.maxSkew || delta > r.maxSkew {
		return Context{}, ErrInvalidAssertion
	}
	want, err := hex.DecodeString(SignAssertion(item.AssertionSecret, item.SiteKey, strings.TrimSpace(timestamp)))
	if err != nil {
		return Context{}, ErrInvalidAssertion
	}
	got, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || !hmac.Equal(got, want) {
		return Context{}, ErrInvalidAssertion
	}
	return item, nil
}

func SignAssertion(secret, siteKey, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	_, _ = mac.Write([]byte(normalize(siteKey) + "\n" + strings.TrimSpace(timestamp)))
	return hex.EncodeToString(mac.Sum(nil))
}

type contextKey struct{}

func With(ctx context.Context, site Context) context.Context {
	return context.WithValue(ctx, contextKey{}, site)
}

func From(ctx context.Context) (Context, bool) {
	item, ok := ctx.Value(contextKey{}).(Context)
	return item, ok && item.SiteKey != ""
}

func (r *Resolver) RequireSite(ctx context.Context, requested string) (string, error) {
	if trusted, ok := From(ctx); ok {
		return trusted.SiteKey, nil
	}
	requested = normalize(requested)
	if r != nil {
		if r.required {
			return "", ErrTrustedContextRequired
		}
		if _, managed := r.bySite[requested]; managed {
			return "", ErrTrustedContextRequired
		}
	}
	return requested, nil
}

func (r *Resolver) Contexts() []Context {
	if r == nil {
		return nil
	}
	out := make([]Context, 0, len(r.bySite))
	for _, item := range r.bySite {
		out = append(out, item)
	}
	return out
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
