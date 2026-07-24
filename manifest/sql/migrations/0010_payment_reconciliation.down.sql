DROP INDEX IF EXISTS ix_payment_attempts_reconcile_due;

ALTER TABLE payment_attempts
    DROP CONSTRAINT IF EXISTS ck_payment_attempts_reconciliation_failures,
    DROP COLUMN IF EXISTS reconciliation_error,
    DROP COLUMN IF EXISTS reconciliation_failures,
    DROP COLUMN IF EXISTS next_reconcile_at,
    DROP COLUMN IF EXISTS last_reconciled_at;
