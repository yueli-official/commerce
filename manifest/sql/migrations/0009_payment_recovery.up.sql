ALTER TABLE orders
    ADD COLUMN payment_state TEXT NOT NULL DEFAULT 'created',
    ADD COLUMN refunded_amount_cents INT NOT NULL DEFAULT 0,
    ADD COLUMN dispute_state TEXT NOT NULL DEFAULT 'none',
    ADD CONSTRAINT ck_orders_payment_state
        CHECK (payment_state IN ('created', 'action_required', 'pending', 'settled', 'failed', 'cancelled')),
    ADD CONSTRAINT ck_orders_refunded_amount
        CHECK (refunded_amount_cents >= 0 AND refunded_amount_cents <= amount_cents),
    ADD CONSTRAINT ck_orders_dispute_state
        CHECK (dispute_state IN ('none', 'open', 'needs_response', 'under_review', 'won', 'lost', 'accepted', 'closed'));

UPDATE orders
SET payment_state = CASE
    WHEN status IN ('paid', 'fulfilled', 'refunding', 'refunded') THEN 'settled'
    WHEN status = 'failed' THEN 'failed'
    WHEN status = 'paying' THEN 'action_required'
    ELSE 'created'
END,
refunded_amount_cents = CASE
    WHEN status = 'refunded' THEN amount_cents
    ELSE 0
END;

ALTER TABLE entitlements
    ADD COLUMN state TEXT NOT NULL DEFAULT 'active',
    ADD COLUMN revoked_at TIMESTAMPTZ,
    ADD COLUMN revoked_reason TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT ck_entitlements_state CHECK (state IN ('active', 'revoked')),
    ADD CONSTRAINT ck_entitlements_revocation CHECK (
        (state = 'active' AND revoked_at IS NULL) OR
        (state = 'revoked' AND revoked_at IS NOT NULL)
    );

CREATE TABLE payment_attempts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    provider            TEXT NOT NULL,
    merchant_account    TEXT NOT NULL,
    idempotency_key     TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'created',
    amount_cents        INT NOT NULL,
    currency            TEXT NOT NULL,
    provider_session_id TEXT NOT NULL DEFAULT '',
    provider_tx_id      TEXT NOT NULL DEFAULT '',
    revision            BIGINT NOT NULL DEFAULT 1,
    last_observed_at    TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_payment_attempts_status
        CHECK (status IN ('created', 'action_required', 'pending', 'settled', 'failed', 'cancelled')),
    CONSTRAINT ck_payment_attempts_amount CHECK (amount_cents > 0),
    CONSTRAINT ck_payment_attempts_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_payment_attempts_revision CHECK (revision > 0),
    CONSTRAINT uq_payment_attempts_idempotency
        UNIQUE (provider, merchant_account, idempotency_key)
);

CREATE INDEX ix_payment_attempts_order_created
    ON payment_attempts(order_id, created_at DESC);
CREATE UNIQUE INDEX uq_payment_attempts_provider_tx
    ON payment_attempts(provider, merchant_account, provider_tx_id)
    WHERE provider_tx_id <> '';

CREATE TABLE refunds (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    payment_attempt_id  UUID REFERENCES payment_attempts(id) ON DELETE SET NULL,
    provider            TEXT NOT NULL,
    merchant_account    TEXT NOT NULL,
    refund_no           TEXT NOT NULL,
    idempotency_key     TEXT NOT NULL,
    provider_refund_id  TEXT NOT NULL DEFAULT '',
    amount_cents        INT NOT NULL,
    currency            TEXT NOT NULL,
    reason              TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'requested',
    requested_by        TEXT NOT NULL,
    revision            BIGINT NOT NULL DEFAULT 1,
    last_observed_at    TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_refunds_status
        CHECK (status IN ('requested', 'submitting', 'pending', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT ck_refunds_amount CHECK (amount_cents > 0),
    CONSTRAINT ck_refunds_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_refunds_revision CHECK (revision > 0),
    CONSTRAINT uq_refunds_refund_no UNIQUE (refund_no),
    CONSTRAINT uq_refunds_idempotency
        UNIQUE (provider, merchant_account, idempotency_key)
);

CREATE INDEX ix_refunds_order_created ON refunds(order_id, created_at DESC);
CREATE INDEX ix_refunds_status_updated ON refunds(status, updated_at);
CREATE UNIQUE INDEX uq_refunds_provider_id
    ON refunds(provider, merchant_account, provider_refund_id)
    WHERE provider_refund_id <> '';

CREATE TABLE disputes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID REFERENCES orders(id) ON DELETE SET NULL,
    payment_attempt_id  UUID REFERENCES payment_attempts(id) ON DELETE SET NULL,
    provider            TEXT NOT NULL,
    merchant_account    TEXT NOT NULL,
    provider_dispute_id TEXT NOT NULL,
    status              TEXT NOT NULL,
    amount_cents        INT NOT NULL DEFAULT 0,
    currency            TEXT NOT NULL DEFAULT '',
    reason_code         TEXT NOT NULL DEFAULT '',
    revision            BIGINT NOT NULL DEFAULT 1,
    opened_at           TIMESTAMPTZ,
    due_at              TIMESTAMPTZ,
    resolved_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_disputes_status
        CHECK (status IN ('open', 'needs_response', 'under_review', 'won', 'lost', 'accepted', 'closed')),
    CONSTRAINT ck_disputes_amount CHECK (amount_cents >= 0),
    CONSTRAINT ck_disputes_currency CHECK (currency = '' OR currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_disputes_revision CHECK (revision > 0),
    CONSTRAINT uq_disputes_provider_id
        UNIQUE (provider, merchant_account, provider_dispute_id)
);

CREATE INDEX ix_disputes_order_updated ON disputes(order_id, updated_at DESC);
CREATE INDEX ix_disputes_status_due ON disputes(status, due_at);

CREATE TABLE provider_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider            TEXT NOT NULL,
    merchant_account    TEXT NOT NULL,
    source              TEXT NOT NULL,
    operation           TEXT NOT NULL,
    idempotency_key     TEXT NOT NULL,
    provider_event_id   TEXT NOT NULL DEFAULT '',
    payload_digest      TEXT NOT NULL,
    provider_status     TEXT NOT NULL,
    normalized_status   TEXT NOT NULL,
    order_no            TEXT NOT NULL DEFAULT '',
    provider_object_id  TEXT NOT NULL DEFAULT '',
    order_id            UUID REFERENCES orders(id) ON DELETE SET NULL,
    payment_attempt_id  UUID REFERENCES payment_attempts(id) ON DELETE SET NULL,
    refund_id           UUID REFERENCES refunds(id) ON DELETE SET NULL,
    dispute_id          UUID REFERENCES disputes(id) ON DELETE SET NULL,
    amount_cents        INT NOT NULL DEFAULT 0,
    currency            TEXT NOT NULL DEFAULT '',
    occurred_at         TIMESTAMPTZ,
    processing_state    TEXT NOT NULL DEFAULT 'received',
    processing_error    TEXT NOT NULL DEFAULT '',
    processed_at        TIMESTAMPTZ,
    received_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_provider_events_source
        CHECK (source IN ('callback', 'query', 'mutation')),
    CONSTRAINT ck_provider_events_operation
        CHECK (operation IN ('payment', 'refund', 'dispute')),
    CONSTRAINT ck_provider_events_digest
        CHECK (payload_digest ~ '^[a-f0-9]{64}$'),
    CONSTRAINT ck_provider_events_amount CHECK (amount_cents >= 0),
    CONSTRAINT ck_provider_events_currency CHECK (currency = '' OR currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_provider_events_processing
        CHECK (processing_state IN ('received', 'applied', 'ignored', 'conflict', 'failed')),
    CONSTRAINT uq_provider_events_idempotency
        UNIQUE (provider, merchant_account, idempotency_key)
);

CREATE INDEX ix_provider_events_order_received
    ON provider_events(order_id, received_at DESC);
CREATE INDEX ix_provider_events_processing_received
    ON provider_events(processing_state, received_at);
