DROP TABLE IF EXISTS provider_events;
DROP TABLE IF EXISTS disputes;
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS payment_attempts;

ALTER TABLE entitlements
    DROP CONSTRAINT IF EXISTS ck_entitlements_revocation,
    DROP CONSTRAINT IF EXISTS ck_entitlements_state,
    DROP COLUMN IF EXISTS revoked_reason,
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS state;

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS ck_orders_dispute_state,
    DROP CONSTRAINT IF EXISTS ck_orders_refunded_amount,
    DROP CONSTRAINT IF EXISTS ck_orders_payment_state,
    DROP COLUMN IF EXISTS dispute_state,
    DROP COLUMN IF EXISTS refunded_amount_cents,
    DROP COLUMN IF EXISTS payment_state;
