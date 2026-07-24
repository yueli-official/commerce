package dao

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/google/uuid"
	"github.com/yueli-official/foundation/go/audit"

	"platform/services/commerce/internal/commerceaudit"
)

func (r *PG) appendRecoveryAudit(
	ctx context.Context,
	tx gdb.TX,
	action commerceaudit.Action,
	target audit.Target,
	evidence commerceaudit.Evidence,
) error {
	if r.audit == nil {
		return nil
	}
	hook := r.audit.Hook(ctx, action, uuid.NewString(), target, evidence)
	return hook(ctx, tx.GetSqlTX())
}
