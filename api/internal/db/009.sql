ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS source_bytes bigint;
