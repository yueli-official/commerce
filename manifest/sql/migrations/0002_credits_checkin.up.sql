-- M2: points economy — credits ledger, daily check-in, points products.

-- Points-priced products carry a points_cost alongside the cents price.
ALTER TABLE products ADD COLUMN points_cost INT NOT NULL DEFAULT 0;

-- Per-subscriber points balance (denormalized snapshot for fast reads + atomic
-- increment/decrement). The ledger below is the append-only source of truth.
CREATE TABLE credits_balances (
    sub        TEXT PRIMARY KEY,
    balance    BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only points ledger: one row per earn (+delta) / spend (-delta).
CREATE TABLE credits_ledger (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sub        TEXT NOT NULL,
    delta      INT NOT NULL,            -- +earn / -spend
    source     TEXT NOT NULL,           -- 'checkin' | 'redeem' | 'grant'
    ref        TEXT NOT NULL DEFAULT '',-- e.g. checkin date / product id
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ix_credits_ledger_sub_created ON credits_ledger (sub, created_at DESC);

-- Daily check-in records. One row per (sub, day); the unique constraint enforces
-- one check-in per calendar day and lets the next day read yesterday's streak.
CREATE TABLE checkin_records (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sub            TEXT NOT NULL,
    checkin_date   DATE NOT NULL,
    streak         INT NOT NULL,
    points_awarded INT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_checkin_sub_date UNIQUE (sub, checkin_date)
);
