ALTER TABLE live_streams ADD COLUMN IF NOT EXISTS unpublished_at timestamptz;
ALTER TABLE live_streams ADD COLUMN IF NOT EXISTS last_hls_at timestamptz;

CREATE UNIQUE INDEX IF NOT EXISTS live_streams_ingest_key_uidx
    ON live_streams (ingest_key)
    WHERE ingest_key IS NOT NULL AND ingest_key <> '';
