package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gogf/gf/v2/frame/g"

	"github.com/yueli-official/commerce/paykit"
	foundationauth "github.com/yueli-official/foundation/go/auth"
	v1 "github.com/yueli-official/commerce/api/v1"
	"github.com/yueli-official/commerce/internal/commerceerr"
	"github.com/yueli-official/commerce/internal/model"
	"github.com/yueli-official/commerce/internal/paymentreconcile"
	"github.com/yueli-official/commerce/internal/service"
	"github.com/yueli-official/commerce/internal/sitecontext"
)

type Checkout struct {
	svc       *service.Service
	registry  paykit.Registry
	notifyURL string
	returnURL string
	sites     *sitecontext.Resolver
	reconcile *paymentreconcile.Reconciler
}

func NewCheckout(svc *service.Service, reg paykit.Registry, notifyURL, returnURL string, sites ...*sitecontext.Resolver) *Checkout {
	return newCheckout(svc, reg, notifyURL, returnURL, nil, sites...)
}

func NewCheckoutWithPaymentReconciler(
	svc *service.Service,
	reg paykit.Registry,
	notifyURL, returnURL string,
	reconciler *paymentreconcile.Reconciler,
	sites ...*sitecontext.Resolver,
) *Checkout {
	return newCheckout(svc, reg, notifyURL, returnURL, reconciler, sites...)
}

func newCheckout(
	svc *service.Service,
	reg paykit.Registry,
	notifyURL, returnURL string,
	reconciler *paymentreconcile.Reconciler,
	sites ...*sitecontext.Resolver,
) *Checkout {
	var siteResolver *sitecontext.Resolver
	if len(sites) > 0 {
		siteResolver = sites[0]
	}
	return &Checkout{
		svc: svc, registry: reg, notifyURL: notifyURL, returnURL: returnURL,
		sites: siteResolver, reconcile: reconciler,
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func (c *Checkout) CreateCheckout(ctx context.Context, req *v1.CreateCheckoutReq) (*v1.CreateCheckoutRes, error) {
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = "alipay"
	}
	if provider == model.ProductKindPoints {
		return nil, commerceerr.InvalidRequest("use /api/v1/checkouts/points for points checkout")
	}
	gw, ok := c.registry[provider]
	if !ok {
		return nil, commerceerr.InvalidRequest("unsupported payment provider")
	}

	desc := service.CheckoutDesc{
		BuyerEmail: strings.TrimSpace(req.BuyerEmail),
		Provider:   provider,
		ReturnURL:  defaultString(c.returnURL, req.ReturnURL),
		CancelURL:  req.CancelURL,
		Items:      make([]service.CheckoutItemDesc, 0, len(req.Items)),
	}
	if p, ok := foundationauth.FromContext(ctx); ok && p != nil {
		desc.BuyerSub = p.Subject
	}
	items, err := checkoutItems(ctx, c.sites, req.Items)
	if err != nil {
		return nil, commerceerr.SiteContextForbidden()
	}
	desc.Items = items

	order, err := c.svc.CreateCheckout(ctx, desc)
	if err != nil {
		return nil, err
	}
	if err := c.svc.PreparePaymentAttempt(ctx, order.OrderNo, provider, "primary"); err != nil {
		return nil, err
	}
	subject := checkoutSubjectFromOrder(ctx, c.svc, order)

	payment, err := gw.CreatePayment(ctx, paykit.CreatePaymentIn{
		OrderNo:     order.OrderNo,
		Subject:     subject,
		AmountCents: order.AmountCents,
		Currency:    order.Currency,
		NotifyURL:   notifyURLFor(provider, c.notifyURL),
		ReturnURL:   defaultString(c.returnURL, req.ReturnURL),
	})
	if err != nil {
		g.Log().Errorf(ctx, "checkout CreatePayment failed for order %s provider %s: %+v", order.OrderNo, provider, err)
		recordPaymentFailure(ctx, c.svc, order, provider, "create_payment", "", err.Error())
		cancelBestEffort(ctx, c.svc, order.OrderNo)
		return nil, commerceerr.GatewayFailed("payment gateway error")
	}
	if payment.SessionID != "" {
		if err := c.svc.SetCheckoutPaymentSession(ctx, order.OrderNo, payment.SessionID); err != nil {
			cancelBestEffort(ctx, c.svc, order.OrderNo)
			return nil, err
		}
	}

	return &v1.CreateCheckoutRes{
		OrderNo: order.OrderNo, AmountCents: order.AmountCents, Currency: order.Currency,
		Provider: payment.Provider, Method: payment.Method, PayURL: payment.PayURL,
		SessionID: payment.SessionID, QRCode: payment.QRCode, ClientToken: payment.ClientToken,
	}, nil
}

func (c *Checkout) CreatePointsCheckout(ctx context.Context, req *v1.CreatePointsCheckoutReq) (*v1.CreatePointsCheckoutRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	items, err := checkoutItems(ctx, c.sites, req.Items)
	if err != nil {
		return nil, commerceerr.SiteContextForbidden()
	}
	res, err := c.svc.RedeemCheckout(ctx, service.CheckoutDesc{
		BuyerSub: p.Subject, BuyerEmail: strings.TrimSpace(req.BuyerEmail), Provider: model.ProductKindPoints,
		Items: items,
	})
	if err != nil {
		return nil, err
	}
	return &v1.CreatePointsCheckoutRes{
		OrderNo: res.Order.OrderNo, Token: res.Grant.Token, DeliveryRef: res.Grant.DeliveryRef,
		State: res.Grant.State, Balance: int(res.Balance),
	}, nil
}

func (c *Checkout) CreateFreeCheckout(ctx context.Context, req *v1.CreateFreeCheckoutReq) (*v1.CreateFreeCheckoutRes, error) {
	var buyerSub string
	if p, ok := foundationauth.FromContext(ctx); ok && p != nil {
		buyerSub = p.Subject
	}
	items, err := checkoutItems(ctx, c.sites, req.Items)
	if err != nil {
		return nil, commerceerr.SiteContextForbidden()
	}
	res, err := c.svc.ClaimFreeCheckout(ctx, service.CheckoutDesc{
		BuyerSub: buyerSub, BuyerEmail: strings.TrimSpace(req.BuyerEmail), Provider: model.ProductKindFree,
		Items: items,
	})
	if err != nil {
		return nil, err
	}
	return &v1.CreateFreeCheckoutRes{
		OrderNo: res.Order.OrderNo, Token: res.Grant.Token, DeliveryRef: res.Grant.DeliveryRef,
		State: res.Grant.State,
	}, nil
}

func (c *Checkout) CaptureCheckout(ctx context.Context, req *v1.CaptureCheckoutReq) (*v1.CaptureCheckoutRes, error) {
	order, err := c.svc.GetOrderByNo(ctx, req.OrderNo)
	if err != nil {
		return nil, err
	}
	if order.Status != model.OrderStatusPaying {
		return nil, commerceerr.OrderInvalidState(order.Status, model.OrderStatusFulfilled)
	}
	if order.PaymentProvider != "" && order.PaymentProvider != req.Provider {
		return nil, commerceerr.InvalidRequest("payment provider mismatch")
	}
	if order.PaymentSessionID != "" && order.PaymentSessionID != req.SessionID {
		return nil, commerceerr.InvalidRequest("payment session mismatch")
	}
	gw, ok := c.registry[req.Provider]
	if !ok {
		return nil, commerceerr.InvalidRequest("unsupported payment provider")
	}
	capture, err := gw.CapturePayment(ctx, paykit.CapturePaymentIn{
		OrderNo: req.OrderNo, SessionID: req.SessionID, AmountCents: order.AmountCents,
	})
	if err != nil {
		c.recordCaptureFailure(ctx, order, req, "", err.Error())
		return nil, commerceerr.GatewayFailed("payment gateway error")
	}
	if !capture.Success {
		c.recordCaptureFailure(ctx, order, req, capture.ProviderTxID, "payment capture failed")
		return nil, commerceerr.NotifyInvalid("payment capture failed")
	}
	amount := capture.AmountCents
	if amount == 0 {
		amount = order.AmountCents
	}
	grant, err := c.svc.SettleCheckout(ctx, req.OrderNo, req.Provider, capture.ProviderTxID, amount)
	if err != nil {
		return nil, err
	}
	return &v1.CaptureCheckoutRes{
		OrderNo: req.OrderNo, Token: grant.Token, DeliveryRef: grant.DeliveryRef, State: grant.State,
	}, nil
}

func (c *Checkout) recordCaptureFailure(ctx context.Context, order *model.Order, req *v1.CaptureCheckoutReq, providerEventID, message string) {
	if order == nil || req == nil {
		return
	}
	recordPaymentFailure(ctx, c.svc, order, req.Provider, "capture", providerEventID, message)
}

func recordPaymentFailure(ctx context.Context, svc *service.Service, order *model.Order, provider, eventType, providerEventID, message string) {
	if order == nil || svc == nil {
		return
	}
	if err := svc.RecordPaymentFailure(ctx, order.OrderNo, provider, eventType, providerEventID, order.AmountCents, message); err != nil {
		g.Log().Warningf(ctx, "record payment failure event failed order=%s provider=%s event=%s: %v", order.OrderNo, provider, eventType, err)
	}
}

func (c *Checkout) CheckoutStatus(ctx context.Context, req *v1.CheckoutStatusReq) (*v1.CheckoutStatusRes, error) {
	var buyerSub string
	if p, ok := foundationauth.FromContext(ctx); ok && p != nil {
		buyerSub = p.Subject
	}
	status, err := c.svc.CheckoutStatus(ctx, req.OrderNo, buyerSub, req.BuyerEmail)
	if err != nil {
		return nil, err
	}
	return checkoutStatusRes(status), nil
}

func (c *Checkout) SyncCheckout(ctx context.Context, req *v1.SyncCheckoutReq) (*v1.SyncCheckoutRes, error) {
	var buyerSub string
	if p, ok := foundationauth.FromContext(ctx); ok && p != nil {
		buyerSub = p.Subject
	}
	status, err := c.svc.CheckoutStatus(ctx, req.OrderNo, buyerSub, req.BuyerEmail)
	if err != nil {
		return nil, err
	}
	order := status.Order
	if order.Status != model.OrderStatusPaying {
		return checkoutStatusRes(status), nil
	}
	if c.reconcile == nil {
		return nil, commerceerr.InvalidRequest("payment reconciliation is not configured")
	}
	if _, err := c.reconcile.ReconcileOrder(ctx, req.OrderNo); err != nil {
		if errors.Is(err, paymentreconcile.ErrQueryUnsupported) {
			return nil, commerceerr.InvalidRequest("payment provider does not support payment query")
		}
		recordPaymentFailure(ctx, c.svc, order, order.PaymentProvider, "query", "", err.Error())
		return nil, commerceerr.GatewayFailed("payment gateway error")
	}
	status, err = c.svc.CheckoutStatus(ctx, req.OrderNo, buyerSub, req.BuyerEmail)
	if err != nil {
		return nil, err
	}
	return checkoutStatusRes(status), nil
}

func (c *Checkout) CancelCheckout(ctx context.Context, req *v1.CancelCheckoutReq) (*v1.CancelCheckoutRes, error) {
	var buyerSub string
	if p, ok := foundationauth.FromContext(ctx); ok && p != nil {
		buyerSub = p.Subject
	}
	order, err := c.svc.CancelCheckout(ctx, req.OrderNo, buyerSub, req.BuyerEmail)
	if err != nil {
		return nil, err
	}
	return &v1.CancelCheckoutRes{
		OrderNo:       order.OrderNo,
		Status:        order.Status,
		DeliveryState: order.DeliveryState,
	}, nil
}

func (c *Checkout) DeliveryByToken(ctx context.Context, req *v1.DeliveryByTokenReq) (*v1.DeliveryByTokenRes, error) {
	delivery, err := c.svc.DeliveryByToken(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	return &v1.DeliveryByTokenRes{Delivery: deliveryView(delivery)}, nil
}

func (c *Checkout) DeliveryDownload(ctx context.Context, req *v1.DeliveryDownloadReq) (*v1.DeliveryDownloadRes, error) {
	download, err := c.svc.ResolveDeliveryDownload(ctx, req.Token, req.Exp, req.Sig)
	if err != nil {
		return nil, err
	}
	return &v1.DeliveryDownloadRes{
		DeliveryRef: download.DeliveryRef,
		URL:         download.URL,
		ExpiresAt:   download.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (c *Checkout) MyPurchases(ctx context.Context, req *v1.MyPurchasesReq) (*v1.MyPurchasesRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	deliveries, total, err := c.svc.Purchases(ctx, service.PurchaseFilter{
		Sub: p.Subject, Q: req.Q, State: req.State,
	}, req.Limit, req.Offset)
	if err != nil {
		return nil, err
	}
	res := &v1.MyPurchasesRes{Purchases: make([]v1.DeliveryView, 0, len(deliveries)), Total: total}
	for _, delivery := range deliveries {
		res.Purchases = append(res.Purchases, deliveryView(delivery))
	}
	return res, nil
}

func (c *Checkout) MyPurchaseByOrder(ctx context.Context, req *v1.MyPurchaseByOrderReq) (*v1.MyPurchaseByOrderRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	delivery, err := c.svc.PurchaseByOrder(ctx, p.Subject, req.OrderNo)
	if err != nil {
		return nil, err
	}
	return &v1.MyPurchaseByOrderRes{Delivery: deliveryView(delivery)}, nil
}

func (c *Checkout) MyPurchaseDownload(ctx context.Context, req *v1.MyPurchaseDownloadReq) (*v1.DeliveryDownloadRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	download, err := c.svc.ResolvePurchaseDownload(ctx, p.Subject, req.OrderNo, req.AssetID)
	if err != nil {
		return nil, err
	}
	return &v1.DeliveryDownloadRes{
		DeliveryRef: download.DeliveryRef,
		URL:         download.URL,
		ExpiresAt:   download.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func checkoutItems(ctx context.Context, sites *sitecontext.Resolver, items []v1.CheckoutItemReq) ([]service.CheckoutItemDesc, error) {
	out := make([]service.CheckoutItemDesc, 0, len(items))
	for _, item := range items {
		siteKey := item.SiteKey
		if sites != nil {
			var err error
			siteKey, err = sites.RequireSite(ctx, siteKey)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, service.CheckoutItemDesc{
			SiteKey: siteKey, ExternalID: item.ExternalID, VariantID: item.VariantID,
			Title: item.Title, VariantTitle: item.VariantTitle, SKU: item.SKU,
			PriceCents: item.PriceCents, PointsCost: item.PointsCost, Currency: item.Currency,
			DeliveryKind: item.DeliveryKind, DeliveryRef: item.DeliveryRef, PurchaseLimitPerBuyer: item.PurchaseLimitPerBuyer, Quantity: item.Quantity,
		})
	}
	return out, nil
}

func deliveryView(delivery *service.DeliveryResult) v1.DeliveryView {
	view := v1.DeliveryView{
		OrderNo: delivery.Order.OrderNo, BuyerEmail: delivery.Order.BuyerEmail, DeliveryRef: delivery.Grant.DeliveryRef,
		State: delivery.Grant.State, CreatedAt: delivery.Grant.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if delivery.DownloadURL != "" {
		view.DownloadURL = delivery.DownloadURL
	}
	if delivery.DownloadExpiresAt != nil {
		view.DownloadExpiresAt = delivery.DownloadExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if delivery.Item != nil {
		view.SiteKey = delivery.Item.SiteKey
		view.ExternalID = delivery.Item.ExternalID
		view.VariantID = delivery.Item.VariantID
		view.Title = delivery.Item.TitleSnapshot
		view.VariantTitle = delivery.Item.VariantTitleSnapshot
		view.SKU = delivery.Item.SKUSnapshot
		view.DeliveryKind = delivery.Item.DeliveryKindSnapshot
	}
	if view.DeliveryKind == "" {
		view.DeliveryKind = "asset_file"
	}
	if view.DeliveryKind == "netdisk" {
		view.Netdisk = parseNetdiskDelivery(delivery.Grant.DeliveryRef)
		view.DeliveryRef = ""
	}
	return view
}

func parseNetdiskDelivery(raw string) *v1.NetdiskDeliveryView {
	var payload struct {
		Netdisk *v1.NetdiskDeliveryView `json:"netdisk"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.Netdisk == nil {
		return nil
	}
	return payload.Netdisk
}

func checkoutStatusRes(status *service.CheckoutStatusResult) *v1.CheckoutStatusRes {
	res := &v1.CheckoutStatusRes{
		OrderNo:       status.Order.OrderNo,
		Status:        status.Order.Status,
		DeliveryState: status.Order.DeliveryState,
	}
	if status.Grant != nil {
		res.DeliveryRef = status.Grant.DeliveryRef
	}
	return res
}

func checkoutSubjectFromOrder(ctx context.Context, svc *service.Service, order *model.Order) string {
	if svc == nil || order == nil {
		return "Virtual goods checkout"
	}
	detail, err := svc.OrderDetail(ctx, order.OrderNo)
	if err != nil || detail == nil || len(detail.Items) == 0 {
		return "订单 " + order.OrderNo
	}
	if len(detail.Items) == 1 {
		item := detail.Items[0]
		if strings.TrimSpace(item.VariantTitleSnapshot) != "" {
			return strings.TrimSpace(item.TitleSnapshot) + " - " + strings.TrimSpace(item.VariantTitleSnapshot)
		}
		if strings.TrimSpace(item.TitleSnapshot) != "" {
			return strings.TrimSpace(item.TitleSnapshot)
		}
		return "订单 " + order.OrderNo
	}
	title := strings.TrimSpace(detail.Items[0].TitleSnapshot)
	if title == "" {
		title = "订单 " + order.OrderNo
	}
	return title + " and more"
}

func notifyURLFor(provider, base string) string {
	if provider == "alipay" {
		return base
	}
	return ""
}

// cancelBestEffort cancels the order on a best-effort basis after payment
// gateway/session failures. The original gateway error remains the response.
func cancelBestEffort(ctx context.Context, svc *service.Service, orderNo string) {
	if err := svc.CancelOrder(ctx, orderNo); err != nil {
		g.Log().Errorf(ctx, "failed to cancel orphan order %s after gateway failure: %+v", orderNo, err)
	}
}
