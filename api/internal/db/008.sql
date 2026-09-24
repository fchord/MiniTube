ALTER TABLE live_streams
    ADD COLUMN IF NOT EXISTS visibility video_visibility NOT NULL DEFAULT 'public';
