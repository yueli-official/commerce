// Package appconfig builds runtime config objects from GoFrame config
// (manifest/config/config.yaml + GF_* env overrides).
package appconfig

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	"platform/services/commerce/internal/assetclient"
	"platform/services/commerce/internal/notificationclient"
	"platform/services/commerce/internal/service"
	"platform/services/commerce/internal/shopclient"
)

// JWKS holds the IdP key/issuer config for the authjwt verifier.
type JWKS struct {
	URL      string
	Issuer   string
	Audience string
}

// LoadJWKS reads commerce.jwks.* from the GoFrame config.
func LoadJWKS(ctx context.Context) JWKS {
	return JWKS{
		URL:      g.Cfg().MustGet(ctx, "commerce.jwks.url").String(),
		Issuer:   g.Cfg().MustGet(ctx, "commerce.jwks.issuer").String(),
		Audience: g.Cfg().MustGet(ctx, "commerce.jwks.audience").String(),
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
		BaseURL:  g.Cfg().MustGet(ctx, "commerce.notificationService.base_url").String(),
		APIToken: g.Cfg().MustGet(ctx, "commerce.notificationService.api_token").String(),
	}
	if cfg.BaseURL == "" {
		g.Log().Warning(ctx, "commerce.notificationService.base_url is empty; delivery notification email disabled")
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
	return service.AssetDeliveryOutput{URL: out.URL, ExpiresAt: out.ExpiresAt}, nil
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
	client *shopclient.Client
}

func (a currentDeliveryAdapter) CurrentDelivery(ctx context.Context, in service.CurrentDeliveryInput) (service.CurrentDeliveryResult, error) {
	out, err := a.client.CurrentDelivery(ctx, shopclient.CurrentDeliveryInput{
		SiteKey: in.SiteKey, ExternalID: in.ExternalID, VariantID: in.VariantID,
	})
	if err != nil {
		return service.CurrentDeliveryResult{}, err
	}
	return service.CurrentDeliveryResult{DeliveryKind: out.DeliveryKind, DeliveryRef: out.DeliveryRef}, nil
}

func (a currentDeliveryAdapter) CurrentCheckoutItem(ctx context.Context, in service.CurrentCheckoutItemInput) (service.CurrentCheckoutItemResult, error) {
	out, err := a.client.CurrentCheckoutItem(ctx, shopclient.CurrentDeliveryInput{
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

func BuildCurrentDeliveryResolver(ctx context.Context) service.CurrentDeliveryResolver {
	cfg := shopclient.Config{
		BaseURL: g.Cfg().MustGet(ctx, "commerce.shopService.base_url").String(),
	}
	if cfg.BaseURL == "" {
		return nil
	}
	client, err := shopclient.New(cfg)
	if err != nil {
		panic(err)
	}
	return currentDeliveryAdapter{client: client}
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
}

// LoadPayPal reads commerce.paypal.* from the GoFrame config.
func LoadPayPal(ctx context.Context) PayPal {
	return PayPal{
		ClientID:     g.Cfg().MustGet(ctx, "commerce.paypal.client_id").String(),
		ClientSecret: g.Cfg().MustGet(ctx, "commerce.paypal.client_secret").String(),
		Sandbox:      g.Cfg().MustGet(ctx, "commerce.paypal.sandbox", true).Bool(),
		BaseURL:      g.Cfg().MustGet(ctx, "commerce.paypal.base_url").String(),
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
