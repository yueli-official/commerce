ALTER TABLE delivery_grants
    DROP COLUMN IF EXISTS download_count,
    DROP COLUMN IF EXISTS max_downloads;
