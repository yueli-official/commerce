package service

import (
	"context"
	"testing"
	"time"

	"github.com/yueli-official/commerce/internal/model"
)

func TestResolveAssetDeliveryUsesCurrentBundleForLatestPolicy(t *testing.T) {
	svc := New(nil, CheckinConfig{})
	assetDelivery := &captureAssetDeliveryForUnit{url: "https://asset.example/latest"}
	svc.ConfigureAssetDeliveryClient(assetDelivery)
	svc.ConfigureCurrentDeliveryResolver(staticCurrentDeliveryResolver{
		out: CurrentDeliveryResult{
			DeliveryKind: "bundle",
			DeliveryRef:  `{"items":[{"kind":"asset_file","assetId":"asset-new","enabled":true}]}`,
		},
	})

	download, err := svc.resolveAssetDelivery(context.Background(), unitDeliveryResult(
		`{"updatePolicy":"latest","items":[{"kind":"asset_file","assetId":"asset-old","enabled":true}]}`,
	), time.Now().UTC().Add(time.Minute), "asset-new")
	if err != nil {
		t.Fatalf("resolveAssetDelivery: %v", err)
	}
	if download.DeliveryRef != "asset-new" {
		t.Fatalf("delivery ref = %q, want asset-new", download.DeliveryRef)
	}
	if assetDelivery.assetID != "asset-new" {
		t.Fatalf("signed asset = %q, want asset-new", assetDelivery.assetID)
	}
}

func TestResolveAssetDeliveryDoesNotUseCurrentBundleForSnapshotPolicy(t *testing.T) {
	svc := New(nil, CheckinConfig{})
	svc.ConfigureCurrentDeliveryResolver(staticCurrentDeliveryResolver{
		out: CurrentDeliveryResult{
			DeliveryKind: "bundle",
			DeliveryRef:  `{"items":[{"kind":"asset_file","assetId":"asset-new","enabled":true}]}`,
		},
	})

	_, err := svc.resolveAssetDelivery(context.Background(), unitDeliveryResult(
		`{"updatePolicy":"snapshot","items":[{"kind":"asset_file","assetId":"asset-old","enabled":true}]}`,
	), time.Now().UTC().Add(time.Minute), "asset-new")
	if err == nil {
		t.Fatal("expected current asset to be rejected for snapshot delivery")
	}
}

func TestDeliveryBundleAccessRulesClampDownloadExpiry(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	got := deliveryBundleAccessRules(`{"access":{"expiresDays":2,"maxDownloads":1,"downloadLinkTTLMin":30}}`, now, 15*time.Minute)
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("expiresAt = %v, want 48 hours from now", got.ExpiresAt)
	}
	if got.MaxDownloads != 1 {
		t.Fatalf("max downloads = %d, want 1", got.MaxDownloads)
	}
	if got.DownloadTTL != 30*time.Minute {
		t.Fatalf("download ttl = %s, want 30m", got.DownloadTTL)
	}
}

func TestDeliveryBundleAccessRulesFallbackToServiceTTL(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	got := deliveryBundleAccessRules(`{"items":[]}`, now, 15*time.Minute)
	if got.ExpiresAt != nil {
		t.Fatalf("expiresAt = %v, want nil", got.ExpiresAt)
	}
	if got.MaxDownloads != 0 {
		t.Fatalf("max downloads = %d, want unlimited", got.MaxDownloads)
	}
	if got.DownloadTTL != 15*time.Minute {
		t.Fatalf("download ttl = %s, want service ttl", got.DownloadTTL)
	}
}

func unitDeliveryResult(ref string) *DeliveryResult {
	return &DeliveryResult{
		Grant: &model.DeliveryGrant{DeliveryRef: ref},
		Order: &model.Order{OrderNo: "CMTEST", BuyerSub: "buyer-sub"},
		Item: &model.OrderItem{
			SiteKey:              "shop",
			ExternalID:           "product-1",
			VariantID:            "variant-1",
			DeliveryKindSnapshot: "bundle",
		},
	}
}

type staticCurrentDeliveryResolver struct {
	out CurrentDeliveryResult
	err error
	in  CurrentDeliveryInput
}

func (r staticCurrentDeliveryResolver) CurrentDelivery(ctx context.Context, in CurrentDeliveryInput) (CurrentDeliveryResult, error) {
	r.in = in
	return r.out, r.err
}

type captureAssetDeliveryForUnit struct {
	url       string
	assetID   string
	subjectID string
}

func (c *captureAssetDeliveryForUnit) CreateDelivery(ctx context.Context, in AssetDeliveryInput) (AssetDeliveryOutput, error) {
	c.assetID = in.AssetID
	c.subjectID = in.SubjectID
	return AssetDeliveryOutput{
		GrantID: "asset-grant-unit", URL: c.url,
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, nil
}
