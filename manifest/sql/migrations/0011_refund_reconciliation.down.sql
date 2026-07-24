DROP INDEX IF EXISTS ix_refunds_reconciliation_due;

ALTER TABLE refunds
    DROP CONSTRAINT IF EXISTS ck_refunds_reconciliation_failures,
    DROP COLUMN IF EXISTS reconciliation_error,
    DROP COLUMN IF EXISTS reconciliation_failures,
    DROP COLUMN IF EXISTS next_reconcile_at,
    DROP COLUMN IF EXISTS last_reconciled_at;
