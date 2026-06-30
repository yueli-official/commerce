CREATE TABLE IF NOT EXISTS commerce_payment_methods (
    provider     TEXT PRIMARY KEY,
    enabled      BOOLEAN NOT NULL DEFAULT false,
    display_name TEXT NOT NULL DEFAULT '',
    sort_order   INT NOT NULL DEFAULT 100,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO commerce_payment_methods (provider, enabled, display_name, sort_order)
VALUES
    ('alipay', true, 'Alipay', 10),
    ('wechat', false, 'WeChat Pay', 20),
    ('paypal', false, 'PayPal', 30)
ON CONFLICT (provider) DO NOTHING;

