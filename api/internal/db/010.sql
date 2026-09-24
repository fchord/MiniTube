ALTER TABLE live_streams
    ADD COLUMN IF NOT EXISTS archive_bytes bigint;
