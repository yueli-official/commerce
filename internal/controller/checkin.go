package controller

import (
	"context"

	foundationauth "github.com/yueli-official/foundation/go/auth"
	v1 "github.com/yueli-official/commerce/api/v1"
	"github.com/yueli-official/commerce/internal/commerceerr"
	"github.com/yueli-official/commerce/internal/service"
)

// Checkin handles the daily check-in endpoints (user JWT required).
type Checkin struct {
	svc *service.Service
}

// NewCheckin constructs a Checkin controller.
func NewCheckin(svc *service.Service) *Checkin { return &Checkin{svc: svc} }

// Do handles POST /api/v1/checkin.
func (c *Checkin) Do(ctx context.Context, _ *v1.CheckinReq) (*v1.CheckinRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	r, err := c.svc.Checkin(ctx, p.Subject)
	if err != nil {
		return nil, err
	}
	return &v1.CheckinRes{
		Date: r.Date, Streak: r.Streak, PointsAwarded: r.PointsAwarded,
		Balance: int(r.Balance), AlreadyCheckedIn: r.AlreadyCheckedIn,
	}, nil
}

// Status handles GET /api/v1/checkin/status.
func (c *Checkin) Status(ctx context.Context, _ *v1.CheckinStatusReq) (*v1.CheckinStatusRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	st, err := c.svc.CheckinStatus(ctx, p.Subject)
	if err != nil {
		return nil, err
	}
	return &v1.CheckinStatusRes{
		CheckedInToday: st.CheckedInToday, Streak: st.Streak, Balance: int(st.Balance),
	}, nil
}
