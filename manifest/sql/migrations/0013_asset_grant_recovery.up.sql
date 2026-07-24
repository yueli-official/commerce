CREATE TABLE asset_delivery_grants (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id          UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    delivery_grant_id UUID NOT NULL REFERENCES delivery_grants(id) ON DELETE CASCADE,
    asset_id          TEXT NOT NULL,
    provider_grant_id TEXT NOT NULL,
    state             TEXT NOT NULL DEFAULT 'active',
    expires_at        TIMESTAMPTZ NOT NULL,
    next_revoke_at    TIMESTAMPTZ,
    revoke_attempts   INT NOT NULL DEFAULT 0,
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at        TIMESTAMPTZ,
    CONSTRAINT uq_asset_delivery_grants_provider UNIQUE (provider_grant_id),
    CONSTRAINT ck_asset_delivery_grants_state
        CHECK (state IN ('active', 'revoke_pending', 'revoked', 'expired'))
);

CREATE INDEX ix_asset_delivery_grants_order
    ON asset_delivery_grants(order_id, created_at DESC);

CREATE INDEX ix_asset_delivery_grants_recovery
    ON asset_delivery_grants(next_revoke_at, created_at)
    WHERE state = 'revoke_pending';
