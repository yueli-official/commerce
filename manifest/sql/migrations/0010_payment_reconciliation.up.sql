ALTER TABLE payment_attempts
    ADD COLUMN last_reconciled_at TIMESTAMPTZ,
    ADD COLUMN next_reconcile_at TIMESTAMPTZ DEFAULT now(),
    ADD COLUMN reconciliation_failures INT NOT NULL DEFAULT 0,
    ADD COLUMN reconciliation_error TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT ck_payment_attempts_reconciliation_failures
        CHECK (reconciliation_failures >= 0);

UPDATE payment_attempts
SET next_reconcile_at = CASE
    WHEN status IN ('created', 'action_required', 'pending') THEN now()
    ELSE NULL
END;

CREATE INDEX ix_payment_attempts_reconcile_due
    ON payment_attempts(next_reconcile_at, updated_at)
    WHERE status IN ('created', 'action_required', 'pending')
      AND next_reconcile_at IS NOT NULL;
