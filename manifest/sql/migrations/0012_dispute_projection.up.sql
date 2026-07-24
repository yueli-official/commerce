ALTER TABLE disputes
    ADD COLUMN provider_tx_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN provider_status TEXT NOT NULL DEFAULT '',
    ADD COLUMN outcome_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN last_observed_at TIMESTAMPTZ;

CREATE INDEX ix_disputes_provider_tx
    ON disputes(provider, merchant_account, provider_tx_id)
    WHERE provider_tx_id <> '';

CREATE UNIQUE INDEX uq_orders_provider_tx
    ON orders(payment_provider, provider_tx_id)
    WHERE payment_provider <> '' AND provider_tx_id <> '';

ALTER TABLE entitlements
    DROP CONSTRAINT ck_entitlements_revocation,
    DROP CONSTRAINT ck_entitlements_state,
    ADD COLUMN suspended_at TIMESTAMPTZ,
    ADD COLUMN suspended_reason TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT ck_entitlements_state
        CHECK (state IN ('active', 'suspended', 'revoked')),
    ADD CONSTRAINT ck_entitlements_lifecycle CHECK (
        (state = 'active' AND suspended_at IS NULL AND revoked_at IS NULL) OR
        (state = 'suspended' AND suspended_at IS NOT NULL AND revoked_at IS NULL) OR
        (state = 'revoked' AND revoked_at IS NOT NULL)
    );

ALTER TABLE delivery_grants
    ADD COLUMN suspended_at TIMESTAMPTZ,
    ADD COLUMN suspended_reason TEXT NOT NULL DEFAULT '';
