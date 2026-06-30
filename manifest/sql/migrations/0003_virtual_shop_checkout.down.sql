DROP TABLE IF EXISTS delivery_grants;
DROP TABLE IF EXISTS payment_events;
DROP TABLE IF EXISTS order_items;

ALTER TABLE orders
    DROP COLUMN IF EXISTS buyer_id,
    DROP COLUMN IF EXISTS buyer_sub,
    DROP COLUMN IF EXISTS buyer_email,
    DROP COLUMN IF EXISTS payment_provider,
    DROP COLUMN IF EXISTS payment_session_id,
    DROP COLUMN IF EXISTS payment_expires_at,
    DROP COLUMN IF EXISTS return_url,
    DROP COLUMN IF EXISTS cancel_url,
    DROP COLUMN IF EXISTS fulfilled_at,
    DROP COLUMN IF EXISTS delivery_state,
    ALTER COLUMN sub SET NOT NULL;

DROP TABLE IF EXISTS commerce_buyers;
