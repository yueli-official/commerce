// Package appconfig builds runtime config objects from GoFrame config
// (manifest/config/config.yaml + GF_* env overrides).
package appconfig

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	"platform/gokit/mail"
	"platform/services/commerce/internal/assetclient"
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

// BuildDeliveryMailer returns the virtual-goods delivery mail transport. SMTP is
// enabled only when commerce.mailer.mode=smtp; otherwise mail is logged in dev.
func BuildDeliveryMailer(ctx context.Context) service.DeliveryMailer {
	var sender mail.Sender
	if g.Cfg().MustGet(ctx, "commerce.mailer.mode").String() == "smtp" {
		s := func(k string) string { return g.Cfg().MustGet(ctx, "commerce.mailer.smtp."+k).String() }
		sender = mail.NewSMTP(s("host"), s("port"), s("username"), s("password"), s("from"), s("fromName"))
	} else {
		sender = mail.NewDev()
	}
	return service.NewDeliveryMailSender(sender)
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
