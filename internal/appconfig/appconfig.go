// Package appconfig builds runtime config objects from GoFrame config
// (manifest/config/config.yaml + GF_* env overrides).
package appconfig

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	"github.com/yueli-official/foundation/go/capability"
	"github.com/yueli-official/notification/client"
	"platform/paykit"
	"platform/services/commerce/internal/assetclient"
	"platform/services/commerce/internal/deliveryrecovery"
	"platform/services/commerce/internal/paymentcap"
	"platform/services/commerce/internal/service"
	"platform/services/commerce/internal/shopclient"
	"platform/services/commerce/internal/sitecontext"
)

// JWKS holds the IdP key/issuer config for the Foundation auth verifier.
type JWKS struct {
	URL               string
	Issuer            string
	Audience          string
	AllowLoopbackHTTP bool
}

// LoadJWKS reads commerce.jwks.* from the GoFrame config.
func LoadJWKS(ctx context.Context) JWKS {
	return JWKS{
		URL:               g.Cfg().MustGet(ctx, "commerce.jwks.url").String(),
		Issuer:            g.Cfg().MustGet(ctx, "commerce.jwks.issuer").String(),
		Audience:          g.Cfg().MustGet(ctx, "commerce.jwks.audience").String(),
		AllowLoopbackHTTP: g.Cfg().MustGet(ctx, "commerce.jwks.allowLoopbackHttp", false).Bool(),
	}
}

// LoadCheckin reads commerce.checkin.* (the daily check-in reward curve).
// Defaults: base 10, +2 per consecutive day, capped at 30.
func LoadCheckin(ctx context.Context) service.CheckinConfig {
	return service.CheckinConfig{
		Base: g.Cfg().MustGet(ctx, "commerce.checkin.base", 10).Int(),
		Step: g.Cfg().MustGet(ctx, "commerce.checkin.step", 2).Int(),
		Cap:  g.Cfg().MustGet(ctx, "commerce.checkin.cap", 30).Int(),
	}
}

// LoadDelivery reads commerce.delivery.* for signed virtual-goods handoff URLs.
func LoadDelivery(ctx context.Context) service.DeliveryConfig {
	ttl := g.Cfg().MustGet(ctx, "commerce.delivery.ttl_seconds", 900).Int()
	return service.DeliveryConfig{
		SigningSecret: g.Cfg().MustGet(ctx, "commerce.delivery.signing_secret").String(),
		PublicBaseURL: g.Cfg().MustGet(ctx, "commerce.delivery.public_base_url").String(),
		TTL:           time.Duration(ttl) * time.Second,
	}
}

// BuildDeliveryMailer returns the virtual-goods delivery notifier. Commerce
// sends a scene notification; provider/channel details live in notification.
func BuildDeliveryMailer(ctx context.Context) service.DeliveryMailer {
	cfg := notificationclient.Config{
		BaseURL:      g.Cfg().MustGet(ctx, "commerce.notificationService.base_url").String(),
		TokenURL:     g.Cfg().MustGet(ctx, "commerce.notificationService.token_url").String(),
		ClientID:     g.Cfg().MustGet(ctx, "commerce.notificationService.client_id").String(),
		ClientSecret: g.Cfg().MustGet(ctx, "commerce.notificationService.client_secret").String(),
		Scope:        g.Cfg().MustGet(ctx, "commerce.notificationService.scope", "notification:send").String(),
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.TokenURL) == "" || strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		g.Log().Warning(ctx, "commerce.notificationService OAuth config is incomplete; delivery notification email disabled")
		return nil
	}
	client, err := notificationclient.New(cfg)
	if err != nil {
		panic(err)
	}
	return notificationDeliverySender{client: client}
}

type notificationDeliverySender struct {
	client *notificationclient.Client
}

func (s notificationDeliverySender) SendDelivery(ctx context.Context, in service.DeliveryMail) error {
	if s.client == nil || !service.DeliveryEmailAvailable(in) {
		return nil
	}
	_, err := s.client.Send(ctx, notificationclient.SendInput{
		IdempotencyKey: "commerce.delivery_ready:" + in.OrderNo + ":" + in.To,
		Scene:          "commerce.delivery_ready",
		Channel:        "email",
		Recipient:      notificationclient.Recipient{Email: in.To},
		Data: map[string]string{
			"orderNo":     in.OrderNo,
			"title":       in.Title,
			"deliveryRef": in.DeliveryRef,
			"deliveryUrl": in.DeliveryURL,
		},
	})
	if err != nil {
		g.Log().Warningf(ctx, "delivery notification failed order=%s to=%s: %v", in.OrderNo, in.To, err)
	}
	return nil
}

type notificationRecoveryNotifier struct {
	client         *notificationclient.Client
	recipientEmail string
}

func BuildRecoveryNotifier(ctx context.Context) deliveryrecovery.FailureNotifier {
	recipientEmail := strings.TrimSpace(
		g.Cfg().MustGet(ctx, "commerce.recovery.notification.recipientEmail").String(),
	)
	if recipientEmail == "" {
		return nil
	}
	cfg := notificationclient.Config{
		BaseURL:      g.Cfg().MustGet(ctx, "commerce.notificationService.base_url").String(),
		TokenURL:     g.Cfg().MustGet(ctx, "commerce.notificationService.token_url").String(),
		ClientID:     g.Cfg().MustGet(ctx, "commerce.notificationService.client_id").String(),
		ClientSecret: g.Cfg().MustGet(ctx, "commerce.notificationService.client_secret").String(),
		Scope:        g.Cfg().MustGet(ctx, "commerce.notificationService.scope", "notification:send").String(),
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.TokenURL) == "" ||
		strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		g.Log().Warning(ctx, "commerce recovery notification configured without Notification OAuth credentials")
		return nil
	}
	client, err := notificationclient.New(cfg)
	if err != nil {
		panic(err)
	}
	return notificationRecoveryNotifier{client: client, recipientEmail: recipientEmail}
}

func (notifier notificationRecoveryNotifier) NotifyDeliveryRevocationFailed(
	ctx context.Context,
	notice deliveryrecovery.FailureNotice,
) {
	if notifier.client == nil || notifier.recipientEmail == "" {
		return
	}
	_, err := notifier.client.Send(ctx, notificationclient.SendInput{
		IdempotencyKey: fmt.Sprintf(
			"commerce.asset_grant_revoke_failed:%s:%d", notice.GrantID, notice.Attempts,
		),
		Scene: "commerce.asset_grant_revoke_failed", Channel: "email",
		Recipient: notificationclient.Recipient{Email: notifier.recipientEmail},
		Data: map[string]string{
			"grantId": notice.GrantID, "orderId": notice.OrderID,
			"providerGrantId": notice.ProviderGrantID,
			"attempts":        strconv.Itoa(notice.Attempts), "error": notice.Error,
		},
	})
	if err != nil {
		g.Log().Warningf(ctx, "commerce recovery notification failed grant=%s: %v", notice.GrantID, err)
	}
}

type assetDeliveryAdapter struct {
	client *assetclient.Client
}

func (a assetDeliveryAdapter) CreateDelivery(ctx context.Context, in service.AssetDeliveryInput) (service.AssetDeliveryOutput, error) {
	out, err := a.client.CreateDelivery(ctx, assetclient.DeliveryInput{
		AssetID: in.AssetID, SubjectID: in.SubjectID, ExpiresIn: in.ExpiresIn, Reason: in.Reason,
	})
	if err != nil {
		return service.AssetDeliveryOutput{}, err
	}
	return service.AssetDeliveryOutput{
		GrantID: out.GrantID, URL: out.URL, ExpiresAt: out.ExpiresAt,
	}, nil
}

func (a assetDeliveryAdapter) RevokeDelivery(ctx context.Context, grantID string) error {
	return a.client.RevokeDelivery(ctx, grantID)
}

func BuildAssetDeliveryClient(ctx context.Context) service.AssetDeliveryClient {
	cfg := assetclient.Config{
		BaseURL:      g.Cfg().MustGet(ctx, "commerce.assetService.base_url").String(),
		TokenURL:     g.Cfg().MustGet(ctx, "commerce.assetService.token_url").String(),
		ClientID:     g.Cfg().MustGet(ctx, "commerce.assetService.client_id").String(),
		ClientSecret: g.Cfg().MustGet(ctx, "commerce.assetService.client_secret").String(),
		Scope:        g.Cfg().MustGet(ctx, "commerce.assetService.scope", "asset:sign").String(),
	}
	if cfg.BaseURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	client, err := assetclient.New(cfg)
	if err != nil {
		panic(err)
	}
	return assetDeliveryAdapter{client: client}
}

type currentDeliveryAdapter struct {
	clients  map[string]*shopclient.Client
	fallback *shopclient.Client
}

func (a currentDeliveryAdapter) client(siteKey string) (*shopclient.Client, error) {
	if client := a.clients[siteKey]; client != nil {
		return client, nil
	}
	if a.fallback != nil {
		return a.fallback, nil
	}
	return nil, fmt.Errorf("shop service is not configured for site %q", siteKey)
}

func (a currentDeliveryAdapter) CurrentDelivery(ctx context.Context, in service.CurrentDeliveryInput) (service.CurrentDeliveryResult, error) {
	client, err := a.client(in.SiteKey)
	if err != nil {
		return service.CurrentDeliveryResult{}, err
	}
	out, err := client.CurrentDelivery(ctx, shopclient.CurrentDeliveryInput{
		SiteKey: in.SiteKey, ExternalID: in.ExternalID, VariantID: in.VariantID,
	})
	if err != nil {
		return service.CurrentDeliveryResult{}, err
	}
	return service.CurrentDeliveryResult{DeliveryKind: out.DeliveryKind, DeliveryRef: out.DeliveryRef}, nil
}

func (a currentDeliveryAdapter) CurrentCheckoutItem(ctx context.Context, in service.CurrentCheckoutItemInput) (service.CurrentCheckoutItemResult, error) {
	client, err := a.client(in.SiteKey)
	if err != nil {
		return service.CurrentCheckoutItemResult{}, err
	}
	out, err := client.CurrentCheckoutItem(ctx, shopclient.CurrentDeliveryInput{
		SiteKey: in.SiteKey, ExternalID: in.ExternalID, VariantID: in.VariantID,
	})
	if err != nil {
		return service.CurrentCheckoutItemResult{}, err
	}
	return service.CurrentCheckoutItemResult{
		SiteKey:               out.SiteKey,
		ExternalID:            out.ExternalID,
		VariantID:             out.VariantID,
		Title:                 out.Title,
		VariantTitle:          out.VariantTitle,
		SKU:                   out.SKU,
		PriceCents:            out.PriceCents,
		PointsCost:            out.PointsCost,
		Currency:              out.Currency,
		DeliveryKind:          out.DeliveryKind,
		DeliveryRef:           out.DeliveryRef,
		PurchaseLimitPerBuyer: out.PurchaseLimitPerBuyer,
	}, nil
}

func LoadSiteContext(ctx context.Context) *sitecontext.Resolver {
	var contexts []sitecontext.Context
	for siteKey, raw := range g.Cfg().MustGet(ctx, "commerce.trustedSiteContexts").Map() {
		value := g.NewVar(raw).Map()
		contexts = append(contexts, sitecontext.Context{
			SiteKey:         siteKey,
			ClientIDs:       g.NewVar(value["clientIds"]).Strings(),
			AssertionSecret: g.NewVar(value["assertionSecret"]).String(),
			ShopBaseURL:     g.NewVar(value["shopBaseUrl"]).String(),
		})
	}
	required := g.Cfg().MustGet(ctx, "commerce.requireTrustedSiteContext", true).Bool()
	return sitecontext.NewWithRequired(contexts, required)
}

func BuildCurrentDeliveryResolver(ctx context.Context, sites ...*sitecontext.Resolver) service.CurrentDeliveryResolver {
	adapter := currentDeliveryAdapter{clients: map[string]*shopclient.Client{}}
	if len(sites) > 0 && sites[0] != nil {
		for _, item := range sites[0].Contexts() {
			if item.ShopBaseURL == "" {
				panic(fmt.Errorf("commerce trusted site %q requires shopBaseUrl", item.SiteKey))
			}
			client, err := shopclient.New(shopclient.Config{BaseURL: item.ShopBaseURL})
			if err != nil {
				panic(err)
			}
			adapter.clients[item.SiteKey] = client
		}
	}
	fallbackURL := g.Cfg().MustGet(ctx, "commerce.shopService.base_url").String()
	if fallbackURL != "" {
		client, err := shopclient.New(shopclient.Config{BaseURL: fallbackURL})
		if err != nil {
			panic(err)
		}
		adapter.fallback = client
	}
	if len(adapter.clients) == 0 && adapter.fallback == nil {
		return nil
	}
	return adapter
}

// Alipay holds the Alipay payment provider config.
type Alipay struct {
	AppID           string
	PrivateKey      string
	AlipayPublicKey string
	Sandbox         bool
	NotifyURL       string
	ReturnURL       string
}

// LoadAlipay reads commerce.alipay.* from the GoFrame config.
func LoadAlipay(ctx context.Context) Alipay {
	return Alipay{
		AppID:           g.Cfg().MustGet(ctx, "commerce.alipay.app_id").String(),
		PrivateKey:      g.Cfg().MustGet(ctx, "commerce.alipay.private_key").String(),
		AlipayPublicKey: g.Cfg().MustGet(ctx, "commerce.alipay.alipay_public_key").String(),
		Sandbox:         g.Cfg().MustGet(ctx, "commerce.alipay.sandbox").Bool(),
		NotifyURL:       g.Cfg().MustGet(ctx, "commerce.alipay.notify_url").String(),
		ReturnURL:       g.Cfg().MustGet(ctx, "commerce.alipay.return_url").String(),
	}
}

// PayPal holds the PayPal Orders API provider config.
type PayPal struct {
	ClientID     string
	ClientSecret string
	Sandbox      bool
	BaseURL      string
	WebhookID    string
}

// LoadPayPal reads commerce.paypal.* from the GoFrame config.
func LoadPayPal(ctx context.Context) PayPal {
	return PayPal{
		ClientID:     g.Cfg().MustGet(ctx, "commerce.paypal.client_id").String(),
		ClientSecret: g.Cfg().MustGet(ctx, "commerce.paypal.client_secret").String(),
		Sandbox:      g.Cfg().MustGet(ctx, "commerce.paypal.sandbox", true).Bool(),
		BaseURL:      g.Cfg().MustGet(ctx, "commerce.paypal.base_url").String(),
		WebhookID:    g.Cfg().MustGet(ctx, "commerce.paypal.webhook_id").String(),
	}
}

// WeChat holds the WeChat Pay APIv3 provider config.
type WeChat struct {
	MerchantID       string
	MerchantCertSN   string
	MerchantAPIv3Key string
	PrivateKey       string
	AppID            string
	NotifyURL        string
}

// LoadWeChat reads commerce.wechat.* from the GoFrame config.
func LoadWeChat(ctx context.Context) WeChat {
	return WeChat{
		MerchantID:       g.Cfg().MustGet(ctx, "commerce.wechat.merchant_id").String(),
		MerchantCertSN:   g.Cfg().MustGet(ctx, "commerce.wechat.merchant_cert_sn").String(),
		MerchantAPIv3Key: g.Cfg().MustGet(ctx, "commerce.wechat.merchant_api_v3_key").String(),
		PrivateKey:       g.Cfg().MustGet(ctx, "commerce.wechat.private_key").String(),
		AppID:            g.Cfg().MustGet(ctx, "commerce.wechat.app_id").String(),
		NotifyURL:        g.Cfg().MustGet(ctx, "commerce.wechat.notify_url").String(),
	}
}

func BuildPaymentCapabilityRegistry(registry paykit.Registry, alipay Alipay, paypal PayPal, wechat WeChat) (*paymentcap.Registry, error) {
	field := func(key, value string, secret bool) capability.ConfigField {
		state := capability.ConfigStateMissing
		if strings.TrimSpace(value) != "" {
			state = capability.ConfigStatePresent
		}
		return capability.ConfigField{Key: key, State: state, Secret: secret}
	}
	mode := func(sandbox bool) string {
		if sandbox {
			return "sandbox"
		}
		return "production"
	}
	gateway := func(key string) paykit.Provider {
		value, _ := registry.Get(key)
		return value
	}
	var (
		alipayGateway = gateway("alipay")
		paypalGateway = gateway("paypal")
		wechatGateway = gateway("wechat")
	)
	paypalOperations := []string{"browser_button", "refund", "server_capture"}
	if strings.TrimSpace(paypal.WebhookID) != "" {
		paypalOperations = append(paypalOperations, "dispute")
	}
	definitions := []paymentcap.Definition{
		paymentcap.Definition{
			Instance: "alipay-primary", Adapter: "alipay", Mode: mode(alipay.Sandbox), Gateway: alipayGateway,
			Operations: []string{"notify", "query", "redirect"}, RequiredConfig: []capability.ConfigField{
				field("app_id", alipay.AppID, false), field("private_key", alipay.PrivateKey, true), field("alipay_public_key", alipay.AlipayPublicKey, false),
				field("notify_url", alipay.NotifyURL, false), field("return_url", alipay.ReturnURL, false),
			},
		},
		paymentcap.Definition{
			Instance: "paypal-primary", Adapter: "paypal", Mode: mode(paypal.Sandbox), Gateway: paypalGateway,
			Operations: paypalOperations, RequiredConfig: []capability.ConfigField{
				field("client_id", paypal.ClientID, false), field("client_secret", paypal.ClientSecret, true),
			},
		},
		paymentcap.Definition{
			Instance: "wechat-primary", Adapter: "wechat", Mode: "production", Gateway: wechatGateway,
			Operations: []string{"native_qr", "notify", "refund"}, RequiredConfig: []capability.ConfigField{
				field("merchant_id", wechat.MerchantID, false), field("merchant_cert_sn", wechat.MerchantCertSN, false),
				field("merchant_api_v3_key", wechat.MerchantAPIv3Key, true), field("private_key", wechat.PrivateKey, true),
				field("app_id", wechat.AppID, false), field("notify_url", wechat.NotifyURL, false),
			},
		},
	}
	if devGateway := gateway("dev"); devGateway != nil {
		definitions = append(definitions, paymentcap.Definition{
			Instance: "dev-local", Adapter: "dev", Mode: "local", Gateway: devGateway,
			Operations: []string{"redirect", "refund"},
		})
	}
	return paymentcap.New(definitions...)
}

func CapabilityServiceMetadata() capability.ServiceMetadata {
	return capability.ServiceMetadata{
		Name: "commerce", Version: envOrAny([]string{"PLATFORM_SERVICE_VERSION", "OTEL_SERVICE_VERSION"}, "dev"),
		BuildSHA:   envOrAny([]string{"PLATFORM_BUILD_SHA", "GITHUB_SHA"}, "unknown"),
		Deployment: envOrAny([]string{"PLATFORM_DEPLOYMENT_IDENTITY", "HOSTNAME"}, "commerce-api"),
	}
}

func CapabilityScope(ctx context.Context) string {
	return g.Cfg().MustGet(ctx, "commerce.capabilityScope", "platform:capabilities:read").String()
}

func CapabilityProbeScope(ctx context.Context) string {
	return g.Cfg().MustGet(ctx, "commerce.capabilityProbeScope", "platform:capabilities:probe").String()
}

func envOrAny(keys []string, fallback string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return fallback
}
