ALTER TABLE refunds
    ADD COLUMN last_reconciled_at TIMESTAMPTZ,
    ADD COLUMN next_reconcile_at TIMESTAMPTZ,
    ADD COLUMN reconciliation_failures INT NOT NULL DEFAULT 0,
    ADD COLUMN reconciliation_error TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT ck_refunds_reconciliation_failures
        CHECK (reconciliation_failures >= 0);

UPDATE refunds
SET next_reconcile_at = now()
WHERE status IN ('submitting', 'pending');

CREATE INDEX ix_refunds_reconciliation_due
    ON refunds(next_reconcile_at, updated_at)
    WHERE status IN ('submitting', 'pending')
      AND next_reconcile_at IS NOT NULL;
