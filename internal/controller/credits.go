package controller

import (
	"context"
	"time"

	foundationauth "github.com/yueli-official/foundation/go/auth"
	v1 "platform/services/commerce/api/v1"
	"platform/services/commerce/internal/commerceerr"
	"platform/services/commerce/internal/service"
)

// Credits handles the points balance/ledger read endpoints (user JWT required).
type Credits struct {
	svc *service.Service
}

// NewCredits constructs a Credits controller.
func NewCredits(svc *service.Service) *Credits { return &Credits{svc: svc} }

// Balance handles GET /api/v1/credits/balance.
func (c *Credits) Balance(ctx context.Context, _ *v1.BalanceReq) (*v1.BalanceRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	bal, err := c.svc.Balance(ctx, p.Subject)
	if err != nil {
		return nil, err
	}
	return &v1.BalanceRes{Balance: int(bal)}, nil
}

// Ledger handles GET /api/v1/credits/ledger.
func (c *Credits) Ledger(ctx context.Context, req *v1.LedgerReq) (*v1.LedgerRes, error) {
	p, ok := foundationauth.FromContext(ctx)
	if !ok || p == nil {
		return nil, commerceerr.Forbidden()
	}
	page, size := req.Page, req.Size
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	entries, total, err := c.svc.Ledger(ctx, p.Subject, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	views := make([]*v1.LedgerEntryView, 0, len(entries))
	for _, e := range entries {
		views = append(views, &v1.LedgerEntryView{
			Delta: e.Delta, Source: e.Source, Ref: e.Ref,
			CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return &v1.LedgerRes{Entries: views, Total: total, Page: page, Size: size}, nil
}
