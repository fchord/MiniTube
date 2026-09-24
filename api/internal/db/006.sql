DO $$ BEGIN
    CREATE TYPE video_visibility AS ENUM ('public', 'private', 'deleted');
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE videos ADD COLUMN IF NOT EXISTS visibility video_visibility NOT NULL DEFAULT 'public';

UPDATE videos SET visibility = 'private', status = 'ready' WHERE status = 'unpublished';
