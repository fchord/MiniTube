ALTER TABLE videos ADD COLUMN IF NOT EXISTS transcode_claimed_at timestamptz;
