package service

import (
	"context"
	"testing"
	"time"

	"platform/services/commerce/internal/model"
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
	return AssetDeliveryOutput{URL: c.url, ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}
