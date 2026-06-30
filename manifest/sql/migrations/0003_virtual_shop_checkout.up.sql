CREATE TABLE commerce_buyers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind             TEXT NOT NULL,
    buyer_sub        TEXT,
    buyer_email      TEXT,
    email_normalized TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_commerce_buyers_sub
    ON commerce_buyers (buyer_sub)
    WHERE buyer_sub IS NOT NULL;

CREATE INDEX ix_commerce_buyers_email
    ON commerce_buyers (email_normalized)
    WHERE email_normalized IS NOT NULL;

ALTER TABLE orders
    ALTER COLUMN sub DROP NOT NULL,
    ADD COLUMN buyer_id UUID REFERENCES commerce_buyers(id),
    ADD COLUMN buyer_sub TEXT,
    ADD COLUMN buyer_email TEXT,
    ADD COLUMN payment_provider TEXT NOT NULL DEFAULT 'alipay',
    ADD COLUMN payment_session_id TEXT,
    ADD COLUMN payment_expires_at TIMESTAMPTZ,
    ADD COLUMN return_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN cancel_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN fulfilled_at TIMESTAMPTZ,
    ADD COLUMN delivery_state TEXT NOT NULL DEFAULT 'pending';

CREATE TABLE order_items (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id               UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    site_key               TEXT NOT NULL,
    external_id            TEXT NOT NULL,
    product_id             UUID REFERENCES products(id),
    variant_id             TEXT NOT NULL DEFAULT '',
    title_snapshot         TEXT NOT NULL,
    variant_title_snapshot TEXT NOT NULL DEFAULT '',
    sku_snapshot           TEXT NOT NULL DEFAULT '',
    quantity               INT NOT NULL DEFAULT 1,
    unit_price_cents       INT NOT NULL DEFAULT 0,
    unit_points_cost       INT NOT NULL DEFAULT 0,
    currency               TEXT NOT NULL DEFAULT 'CNY',
    delivery_kind_snapshot TEXT NOT NULL DEFAULT 'asset_file',
    delivery_ref_snapshot  TEXT NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ix_order_items_order ON order_items(order_id);

CREATE TABLE payment_events (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id          UUID REFERENCES orders(id) ON DELETE SET NULL,
    provider          TEXT NOT NULL,
    event_type        TEXT NOT NULL,
    provider_event_id TEXT NOT NULL DEFAULT '',
    raw_hash          TEXT NOT NULL DEFAULT '',
    amount_cents      INT NOT NULL DEFAULT 0,
    success           BOOLEAN NOT NULL DEFAULT false,
    message           TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ix_payment_events_order_created ON payment_events(order_id, created_at DESC);

CREATE TABLE delivery_grants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    order_item_id UUID REFERENCES order_items(id) ON DELETE CASCADE,
    buyer_sub     TEXT,
    buyer_email   TEXT,
    token_hash    TEXT NOT NULL,
    delivery_ref  TEXT NOT NULL,
    state         TEXT NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX uq_delivery_grants_token_hash ON delivery_grants(token_hash);
CREATE INDEX ix_delivery_grants_order ON delivery_grants(order_id);
CREATE INDEX ix_delivery_grants_sub ON delivery_grants(buyer_sub) WHERE buyer_sub IS NOT NULL;
