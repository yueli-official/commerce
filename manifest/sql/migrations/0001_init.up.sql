CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    site_key    TEXT NOT NULL,
    external_id TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'paid',
    title       TEXT NOT NULL DEFAULT '',
    price_cents INT NOT NULL DEFAULT 0,
    currency    TEXT NOT NULL DEFAULT 'CNY',
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_products_site_external UNIQUE (site_key, external_id)
);

CREATE TABLE orders (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_no       TEXT NOT NULL,
    sub            TEXT NOT NULL,
    product_id     UUID NOT NULL REFERENCES products(id),
    amount_cents   INT NOT NULL,
    currency       TEXT NOT NULL DEFAULT 'CNY',
    status         TEXT NOT NULL DEFAULT 'pending',
    gateway        TEXT NOT NULL DEFAULT 'alipay',
    provider_tx_id TEXT,
    paid_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_orders_order_no UNIQUE (order_no)
);

CREATE INDEX ix_orders_sub_created ON orders (sub, created_at);
CREATE INDEX ix_orders_status      ON orders (status);

CREATE TABLE entitlements (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sub        TEXT NOT NULL,
    product_id UUID NOT NULL REFERENCES products(id),
    source     TEXT NOT NULL DEFAULT 'order',
    order_id   UUID REFERENCES orders(id),
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    CONSTRAINT uq_entitlements_sub_product UNIQUE (sub, product_id)
);

CREATE INDEX ix_entitlements_sub ON entitlements (sub);
