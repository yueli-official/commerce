UPDATE entitlements
SET state = 'revoked',
    revoked_at = COALESCE(revoked_at, now()),
    revoked_reason = CASE
        WHEN revoked_reason = '' THEN suspended_reason
        ELSE revoked_reason
    END
WHERE state = 'suspended';

UPDATE delivery_grants
SET state = 'revoked',
    revoked_at = COALESCE(revoked_at, now())
WHERE state = 'suspended';

UPDATE orders
SET delivery_state = 'revoked'
WHERE delivery_state = 'suspended';

ALTER TABLE delivery_grants
    DROP COLUMN IF EXISTS suspended_reason,
    DROP COLUMN IF EXISTS suspended_at;

ALTER TABLE entitlements
    DROP CONSTRAINT IF EXISTS ck_entitlements_lifecycle,
    DROP CONSTRAINT IF EXISTS ck_entitlements_state,
    DROP COLUMN IF EXISTS suspended_reason,
    DROP COLUMN IF EXISTS suspended_at,
    ADD CONSTRAINT ck_entitlements_state CHECK (state IN ('active', 'revoked')),
    ADD CONSTRAINT ck_entitlements_revocation CHECK (
        (state = 'active' AND revoked_at IS NULL) OR
        (state = 'revoked' AND revoked_at IS NOT NULL)
    );

DROP INDEX IF EXISTS uq_orders_provider_tx;
DROP INDEX IF EXISTS ix_disputes_provider_tx;

ALTER TABLE disputes
    DROP COLUMN IF EXISTS last_observed_at,
    DROP COLUMN IF EXISTS outcome_code,
    DROP COLUMN IF EXISTS provider_status,
    DROP COLUMN IF EXISTS provider_tx_id;
