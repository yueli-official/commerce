// Package service implements the commerce domain logic:
// product lazy-create, order state machine, entitlement grant.
package service

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/os/gtime"

	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/dao"
	"platform/services/commerce/internal/model"
)

// OrderDesc carries the caller-supplied descriptor for a new order. SiteKey +
// ExternalID identify the product; PriceCents (paid) / PointsCost (points) is the
// buyer-confirmed price in the kind's unit.
type OrderDesc struct {
	SiteKey    string
	ExternalID string
	Kind       string
	Title      string
	Currency   string
	PriceCents int
	PointsCost int
}

// EntitledResult is the answer to an Entitled query.
type EntitledResult struct {
	Entitled bool
	Reason   string // "ok" | "not_purchased" | "insufficient_points" | "expired"
	Required struct {
		Kind       string
		PriceCents *int // paid: snapshot price; nil when product not yet created
		PointsCost *int // points: snapshot cost; nil otherwise
	}
}

// CheckinConfig is the daily check-in reward curve (config-driven). Points for a
// streak of N = Base + (N-1)*Step, capped at Cap; a missed day resets the streak.
type CheckinConfig struct {
	Base int
	Step int
	Cap  int
}

// Service holds the DAO and implements the commerce domain logic.
type Service struct {
	db      *dao.PG
	checkin CheckinConfig
}

// New constructs a Service.
func New(db *dao.PG, checkin CheckinConfig) *Service {
	return &Service{db: db, checkin: checkin}
}

// CreateOrder lazily upserts the product from desc, then inserts a new order in
// "pending" status. Returns both the created order and the resolved product.
func (s *Service) CreateOrder(ctx context.Context, sub string, desc OrderDesc) (*model.Order, *model.Product, error) {
	// Lazy upsert product.
	p := &model.Product{
		SiteKey:    desc.SiteKey,
		ExternalID: desc.ExternalID,
		Kind:       desc.Kind,
		Title:      desc.Title,
		PriceCents: desc.PriceCents,
		Currency:   desc.Currency,
		Status:     model.ProductStatusActive,
	}
	if err := s.db.UpsertProduct(ctx, p); err != nil {
		return nil, nil, err
	}

	// Build and insert the order.
	o := &model.Order{
		OrderNo:     NewOrderNo(),
		Sub:         sub,
		ProductID:   p.ID,
		AmountCents: desc.PriceCents,
		Currency:    desc.Currency,
		Status:      model.OrderStatusPending,
	}
	if err := s.db.InsertOrder(ctx, o); err != nil {
		return nil, nil, err
	}
	return o, p, nil
}

// SetPaying transitions an order from "pending" to "paying".
// Called after the payment gateway creates a payment session.
func (s *Service) SetPaying(ctx context.Context, orderNo string) error {
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	if o == nil {
		return commerceerr.OrderNotFound(orderNo)
	}
	if o.Status != model.OrderStatusPending {
		return commerceerr.OrderInvalidState(o.Status, model.OrderStatusPaying)
	}
	return s.db.UpdateOrderStatus(ctx, o.ID, model.OrderStatusPaying)
}

// MarkPaid transitions an order from "paying" to "paid" and grants the entitlement.
// The operation is idempotent: if the order is already "paid", it returns nil without
// re-granting. Amount mismatch returns commerce.notify_invalid.
// Illegal source state (not "paying" and not already "paid") returns commerce.order_invalid_state.
// The status update and entitlement insert are committed in a single transaction.
func (s *Service) MarkPaid(ctx context.Context, orderNo, providerTxID string, amountCents int) error {
	// Load order outside the transaction — if it doesn't exist we bail early.
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	if o == nil {
		return commerceerr.OrderNotFound(orderNo)
	}

	// Idempotent: already paid → success, no re-grant.
	if o.Status == model.OrderStatusPaid {
		return nil
	}

	// Only "paying" orders may be marked paid.
	if o.Status != model.OrderStatusPaying {
		return commerceerr.OrderInvalidState(o.Status, model.OrderStatusPaid)
	}

	// Amount guard.
	if amountCents != o.AmountCents {
		return commerceerr.NotifyInvalid("amount mismatch")
	}

	// Transact: mark paid + grant entitlement.
	// The UPDATE is conditional on status='paying' so that a concurrent delivery
	// that already won the race leaves 0 rows affected → we return nil (safe).
	now := gtime.New(time.Now())
	return s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		updated, err := s.db.ConditionalUpdateOrderStatusTx(
			ctx, tx, o.ID,
			model.OrderStatusPaying, model.OrderStatusPaid,
			providerTxID, now,
		)
		if err != nil {
			return err
		}
		if !updated {
			// Another concurrent delivery already transitioned this order; safe to no-op.
			return nil
		}
		ent := &model.Entitlement{
			Sub:       o.Sub,
			ProductID: o.ProductID,
			Source:    "order",
			OrderID:   &o.ID,
		}
		return s.db.InsertEntitlementTx(ctx, tx, ent)
	})
}

// CancelOrder transitions an order from "pending" or "paying" to "cancelled".
// It is intentionally lenient: if the order is already in a terminal state
// (paid, cancelled) or does not exist, it returns nil (safe to call best-effort).
func (s *Service) CancelOrder(ctx context.Context, orderNo string) error {
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return err
	}
	if o == nil {
		// Order not found — nothing to cancel.
		return nil
	}
	if o.Status != model.OrderStatusPending && o.Status != model.OrderStatusPaying {
		// Already in a terminal state; no-op.
		return nil
	}
	return s.db.UpdateOrderStatus(ctx, o.ID, model.OrderStatusCancelled)
}

// GetOrderByNo returns the order with the given order number, or an error if not found.
// Used by the dev-settle endpoint to retrieve the order's recorded amount.
func (s *Service) GetOrderByNo(ctx context.Context, orderNo string) (*model.Order, error) {
	o, err := s.db.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, commerceerr.OrderNotFound(orderNo)
	}
	return o, nil
}

// Entitled reports whether sub is entitled to the product identified by (siteKey, externalID).
// Reason is "ok" when entitled, otherwise "not_purchased" (no entitlement, or the product
// has never been created).
func (s *Service) Entitled(ctx context.Context, sub, siteKey, externalID string) (EntitledResult, error) {
	var result EntitledResult

	p, err := s.db.GetProductByExternal(ctx, siteKey, externalID)
	if err != nil {
		return result, err
	}

	// Product not yet created (no one has ever ordered it).
	if p == nil {
		result.Entitled = false
		result.Reason = "not_purchased"
		result.Required.Kind = ""
		result.Required.PriceCents = nil
		return result, nil
	}

	// Fill in the product snapshot for the caller (price in the kind's unit).
	result.Required.Kind = p.Kind
	if p.Kind == model.ProductKindPoints {
		pts := p.PointsCost
		result.Required.PointsCost = &pts
	} else {
		pc := p.PriceCents
		result.Required.PriceCents = &pc
	}

	exists, err := s.db.EntitlementExists(ctx, sub, p.ID)
	if err != nil {
		return result, err
	}
	if exists {
		result.Entitled = true
		result.Reason = "ok"
		return result, nil
	}
	result.Entitled = false
	result.Reason = "not_purchased"
	// For a points product the user doesn't own, distinguish "can afford / can't"
	// so the button can read "兑换" vs "积分不足".
	if p.Kind == model.ProductKindPoints {
		bal, err := s.db.GetBalance(ctx, sub)
		if err != nil {
			return result, err
		}
		if bal < int64(p.PointsCost) {
			result.Reason = "insufficient_points"
		}
	}
	return result, nil
}

// ─── M2: check-in + credits + points redemption ─────────────────────────────

// checkinPoints is the reward for a streak of n consecutive days.
func (s *Service) checkinPoints(streak int) int {
	p := s.checkin.Base + (streak-1)*s.checkin.Step
	if s.checkin.Cap > 0 && p > s.checkin.Cap {
		p = s.checkin.Cap
	}
	if p < 0 {
		p = 0
	}
	return p
}

// CheckinResult is the outcome of a daily check-in.
type CheckinResult struct {
	Date             string
	Streak           int
	PointsAwarded    int
	Balance          int64
	AlreadyCheckedIn bool
}

// Checkin records today's check-in for sub and credits the streak reward.
// Idempotent per UTC day: a second call the same day earns nothing and reports
// AlreadyCheckedIn. The streak continues yesterday's (or resets to 1).
func (s *Service) Checkin(ctx context.Context, sub string) (CheckinResult, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Fast path: already checked in today (no write).
	existing, err := s.db.GetCheckin(ctx, sub, today)
	if err != nil {
		return CheckinResult{}, err
	}
	if existing != nil {
		bal, err := s.db.GetBalance(ctx, sub)
		if err != nil {
			return CheckinResult{}, err
		}
		return CheckinResult{Date: today, Streak: existing.Streak, PointsAwarded: existing.PointsAwarded, Balance: bal, AlreadyCheckedIn: true}, nil
	}

	streak := 1
	y, err := s.db.GetCheckin(ctx, sub, yesterday)
	if err != nil {
		return CheckinResult{}, err
	}
	if y != nil {
		streak = y.Streak + 1
	}
	points := s.checkinPoints(streak)

	already := false
	if err := s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		inserted, err := s.db.InsertCheckinTx(ctx, tx, &model.CheckinRecord{
			Sub: sub, CheckinDate: today, Streak: streak, PointsAwarded: points,
		})
		if err != nil {
			return err
		}
		if !inserted {
			already = true // a concurrent check-in won the race; don't double-earn
			return nil
		}
		return s.db.EarnCreditsTx(ctx, tx, sub, points, model.CreditsSourceCheckin, today)
	}); err != nil {
		return CheckinResult{}, err
	}

	bal, err := s.db.GetBalance(ctx, sub)
	if err != nil {
		return CheckinResult{}, err
	}
	if already {
		rec, err := s.db.GetCheckin(ctx, sub, today)
		if err != nil {
			return CheckinResult{}, err
		}
		res := CheckinResult{Date: today, Balance: bal, AlreadyCheckedIn: true}
		if rec != nil {
			res.Streak, res.PointsAwarded = rec.Streak, rec.PointsAwarded
		}
		return res, nil
	}
	return CheckinResult{Date: today, Streak: streak, PointsAwarded: points, Balance: bal}, nil
}

// Balance returns the subscriber's current points balance.
func (s *Service) Balance(ctx context.Context, sub string) (int64, error) {
	return s.db.GetBalance(ctx, sub)
}

// Ledger returns a page of the subscriber's points ledger plus the total count.
func (s *Service) Ledger(ctx context.Context, sub string, limit, offset int) ([]model.LedgerEntry, int, error) {
	return s.db.ListLedger(ctx, sub, limit, offset)
}

// CheckinStatus is today's check-in state for a subscriber (read-only).
type CheckinStatus struct {
	CheckedInToday bool
	Streak         int
	Balance        int64
}

// CheckinStatus reports whether sub has checked in today (+ current streak +
// balance) without mutating anything — drives the check-in button.
func (s *Service) CheckinStatus(ctx context.Context, sub string) (CheckinStatus, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	rec, err := s.db.GetCheckin(ctx, sub, today)
	if err != nil {
		return CheckinStatus{}, err
	}
	bal, err := s.db.GetBalance(ctx, sub)
	if err != nil {
		return CheckinStatus{}, err
	}
	st := CheckinStatus{Balance: bal}
	if rec != nil {
		st.CheckedInToday = true
		st.Streak = rec.Streak
	}
	return st, nil
}

// RedeemResult is the outcome of a points redemption.
type RedeemResult struct {
	Entitled bool
	Balance  int64
}

// Redeem spends points to grant sub an entitlement to the product in desc.
// Idempotent: a repeat redeem on an already-owned product grants and spends
// nothing (the fresh-grant check gates the spend, so points are never
// double-charged). Insufficient balance → commerce.insufficient_points.
func (s *Service) Redeem(ctx context.Context, sub string, desc OrderDesc) (RedeemResult, error) {
	p := &model.Product{
		SiteKey: desc.SiteKey, ExternalID: desc.ExternalID, Kind: model.ProductKindPoints,
		Title: desc.Title, PointsCost: desc.PointsCost, Currency: desc.Currency, Status: model.ProductStatusActive,
	}
	if err := s.db.UpsertProduct(ctx, p); err != nil {
		return RedeemResult{}, err
	}
	if err := s.db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		inserted, err := s.db.GrantEntitlementTx(ctx, tx, &model.Entitlement{
			Sub: sub, ProductID: p.ID, Source: model.EntitlementSourcePoints,
		})
		if err != nil {
			return err
		}
		if !inserted {
			return nil // already owned: grant + spend are no-ops
		}
		ok, err := s.db.SpendCreditsTx(ctx, tx, sub, desc.PointsCost, model.CreditsSourceRedeem, p.ID)
		if err != nil {
			return err
		}
		if !ok {
			return commerceerr.InsufficientPoints(desc.PointsCost)
		}
		return nil
	}); err != nil {
		return RedeemResult{}, err
	}
	bal, err := s.db.GetBalance(ctx, sub)
	if err != nil {
		return RedeemResult{}, err
	}
	return RedeemResult{Entitled: true, Balance: bal}, nil
}
