-- Refuse an ambiguous merge. Product ids are referenced by orders and
-- entitlements, so silently deleting either row would corrupt ownership.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM products legacy
        JOIN products target
          ON target.external_id = legacy.external_id
         AND target.site_key = CASE legacy.site_key
             WHEN 'shop' THEN 'shop-ae'
             WHEN 'resource' THEN 'resource-ae'
         END
        WHERE legacy.site_key IN ('shop', 'resource')
    ) THEN
        RAISE EXCEPTION 'commerce site context migration has duplicate target products';
    END IF;
END $$;

UPDATE products
SET site_key = CASE site_key
    WHEN 'shop' THEN 'shop-ae'
    WHEN 'resource' THEN 'resource-ae'
    ELSE site_key
END,
updated_at = now()
WHERE site_key IN ('shop', 'resource');

UPDATE order_items
SET site_key = CASE site_key
    WHEN 'shop' THEN 'shop-ae'
    WHEN 'resource' THEN 'resource-ae'
    ELSE site_key
END
WHERE site_key IN ('shop', 'resource');

