CREATE OR REPLACE FUNCTION minitube_migrate_014() RETURNS void LANGUAGE plpgsql AS $mt014$
DECLARE
  con record;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'public' AND table_name = 'videos'
      AND column_name = 'id' AND udt_name = 'uuid'
  ) THEN
    RETURN;
  END IF;

  ALTER TABLE videos ADD COLUMN IF NOT EXISTS new_id text;

  CREATE OR REPLACE FUNCTION minitube_new_video_id() RETURNS text AS $vid$
  DECLARE
    chars constant text := 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
    out text;
    i int;
  BEGIN
    LOOP
      out := '';
      FOR i IN 1..10 LOOP
        out := out || substr(chars, 1 + (floor(random() * 62))::int, 1);
      END LOOP;
      EXIT WHEN NOT EXISTS (SELECT 1 FROM videos WHERE new_id = out);
    END LOOP;
    RETURN out;
  END;
  $vid$ LANGUAGE plpgsql;

  UPDATE videos SET new_id = minitube_new_video_id()
  WHERE new_id IS NULL OR new_id = '';

  ALTER TABLE videos ALTER COLUMN new_id SET NOT NULL;
  CREATE UNIQUE INDEX IF NOT EXISTS videos_new_id_uidx ON videos (new_id);

  CREATE TABLE IF NOT EXISTS video_id_legacy (
      old_id uuid PRIMARY KEY,
      video_id text NOT NULL
  );

  INSERT INTO video_id_legacy (old_id, video_id)
  SELECT id, new_id FROM videos
  ON CONFLICT (old_id) DO NOTHING;


ALTER TABLE comments ALTER COLUMN target_id TYPE text USING target_id::text;
ALTER TABLE likes ALTER COLUMN target_id TYPE text USING target_id::text;
ALTER TABLE favorite_items ALTER COLUMN target_id TYPE text USING target_id::text;

UPDATE comments c
SET target_id = v.new_id
FROM videos v
WHERE c.target_type = 'video' AND c.target_id = v.id::text;

UPDATE likes l
SET target_id = v.new_id
FROM videos v
WHERE l.target_type = 'video' AND l.target_id = v.id::text;

UPDATE favorite_items f
SET target_id = v.new_id
FROM videos v
WHERE f.target_type = 'video' AND f.target_id = v.id::text;

  FOR con IN
    SELECT conrelid::regclass AS tbl, conname
    FROM pg_constraint
    WHERE confrelid = 'videos'::regclass AND contype = 'f'
  LOOP
    EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', con.tbl, con.conname);
  END LOOP;

ALTER TABLE video_renditions ADD COLUMN video_id2 text;
UPDATE video_renditions vr SET video_id2 = v.new_id FROM videos v WHERE v.id = vr.video_id;
ALTER TABLE video_renditions DROP COLUMN video_id;
ALTER TABLE video_renditions RENAME COLUMN video_id2 TO video_id;
ALTER TABLE video_renditions ALTER COLUMN video_id SET NOT NULL;
ALTER TABLE video_renditions ADD CONSTRAINT video_renditions_video_id_height_key UNIQUE (video_id, height);

ALTER TABLE audio_tracks ADD COLUMN video_id2 text;
UPDATE audio_tracks atk SET video_id2 = v.new_id FROM videos v WHERE v.id = atk.video_id;
DROP INDEX IF EXISTS audio_tracks_one_default_per_video;
ALTER TABLE audio_tracks DROP COLUMN video_id;
ALTER TABLE audio_tracks RENAME COLUMN video_id2 TO video_id;
ALTER TABLE audio_tracks ALTER COLUMN video_id SET NOT NULL;
ALTER TABLE audio_tracks ADD CONSTRAINT audio_tracks_video_id_language_label_key UNIQUE (video_id, language, label);
CREATE UNIQUE INDEX audio_tracks_one_default_per_video ON audio_tracks (video_id) WHERE is_default;

ALTER TABLE subtitle_tracks ADD COLUMN video_id2 text;
UPDATE subtitle_tracks st SET video_id2 = v.new_id FROM videos v WHERE v.id = st.video_id;
ALTER TABLE subtitle_tracks DROP COLUMN video_id;
ALTER TABLE subtitle_tracks RENAME COLUMN video_id2 TO video_id;
ALTER TABLE subtitle_tracks ALTER COLUMN video_id SET NOT NULL;
ALTER TABLE subtitle_tracks ADD CONSTRAINT subtitle_tracks_video_id_language_label_key UNIQUE (video_id, language, label);

ALTER TABLE watch_history ADD COLUMN video_id2 text;
UPDATE watch_history wh SET video_id2 = v.new_id FROM videos v WHERE v.id = wh.video_id;
ALTER TABLE watch_history DROP CONSTRAINT watch_history_pkey;
ALTER TABLE watch_history DROP COLUMN video_id;
ALTER TABLE watch_history RENAME COLUMN video_id2 TO video_id;
ALTER TABLE watch_history ALTER COLUMN video_id SET NOT NULL;
ALTER TABLE watch_history ADD PRIMARY KEY (user_id, video_id);

ALTER TABLE live_streams ADD COLUMN vod_video_id2 text;
UPDATE live_streams ls SET vod_video_id2 = v.new_id FROM videos v WHERE v.id = ls.vod_video_id;
ALTER TABLE live_streams DROP COLUMN vod_video_id;
ALTER TABLE live_streams RENAME COLUMN vod_video_id2 TO vod_video_id;

ALTER TABLE posts ADD COLUMN mentioned_video_id2 text;
UPDATE posts p SET mentioned_video_id2 = v.new_id FROM videos v WHERE v.id = p.mentioned_video_id;
ALTER TABLE posts DROP COLUMN mentioned_video_id;
ALTER TABLE posts RENAME COLUMN mentioned_video_id2 TO mentioned_video_id;

ALTER TABLE transcode_workers ADD COLUMN claimed_video_id2 text;
UPDATE transcode_workers tw SET claimed_video_id2 = v.new_id FROM videos v WHERE v.id = tw.claimed_video_id;
ALTER TABLE transcode_workers DROP COLUMN claimed_video_id;
ALTER TABLE transcode_workers RENAME COLUMN claimed_video_id2 TO claimed_video_id;

ALTER TABLE videos DROP CONSTRAINT videos_pkey;
ALTER TABLE videos DROP COLUMN id;
ALTER TABLE videos RENAME COLUMN new_id TO id;
ALTER TABLE videos ADD PRIMARY KEY (id);
ALTER TABLE videos ADD CONSTRAINT videos_id_fmt CHECK (id ~ '^[A-Za-z0-9]{10}$');
DROP INDEX IF EXISTS videos_new_id_uidx;

ALTER TABLE video_renditions
  ADD CONSTRAINT video_renditions_video_id_fkey FOREIGN KEY (video_id) REFERENCES videos (id) ON DELETE CASCADE;
ALTER TABLE audio_tracks
  ADD CONSTRAINT audio_tracks_video_id_fkey FOREIGN KEY (video_id) REFERENCES videos (id) ON DELETE CASCADE;
ALTER TABLE subtitle_tracks
  ADD CONSTRAINT subtitle_tracks_video_id_fkey FOREIGN KEY (video_id) REFERENCES videos (id) ON DELETE CASCADE;
ALTER TABLE watch_history
  ADD CONSTRAINT watch_history_video_id_fkey FOREIGN KEY (video_id) REFERENCES videos (id) ON DELETE CASCADE;
ALTER TABLE live_streams
  ADD CONSTRAINT live_streams_vod_video_id_fkey FOREIGN KEY (vod_video_id) REFERENCES videos (id) ON DELETE SET NULL;
ALTER TABLE posts
  ADD CONSTRAINT posts_mentioned_video_id_fkey FOREIGN KEY (mentioned_video_id) REFERENCES videos (id) ON DELETE SET NULL;
ALTER TABLE transcode_workers
  ADD CONSTRAINT transcode_workers_claimed_video_id_fkey FOREIGN KEY (claimed_video_id) REFERENCES videos (id) ON DELETE SET NULL;
ALTER TABLE video_id_legacy
  ADD CONSTRAINT video_id_legacy_video_id_fkey FOREIGN KEY (video_id) REFERENCES videos (id) ON DELETE CASCADE;

UPDATE video_renditions r
SET playlist_key = replace(r.playlist_key, m.old_id::text, m.video_id)
FROM video_id_legacy m
WHERE r.video_id = m.video_id AND position(m.old_id::text IN r.playlist_key) > 0;

UPDATE audio_tracks a
SET playlist_key = replace(a.playlist_key, m.old_id::text, m.video_id)
FROM video_id_legacy m
WHERE a.video_id = m.video_id AND position(m.old_id::text IN a.playlist_key) > 0;

UPDATE subtitle_tracks s
SET vtt_key = replace(s.vtt_key, m.old_id::text, m.video_id)
FROM video_id_legacy m
WHERE s.video_id = m.video_id AND position(m.old_id::text IN s.vtt_key) > 0;

UPDATE videos v
SET source_object_key = replace(v.source_object_key, m.old_id::text, m.video_id),
    thumbnail_url = replace(v.thumbnail_url, m.old_id::text, m.video_id),
    preview_sprite_url = replace(v.preview_sprite_url, m.old_id::text, m.video_id)
FROM video_id_legacy m
WHERE v.id = m.video_id;

  DROP FUNCTION IF EXISTS minitube_new_video_id();
END;
$mt014$;

SELECT minitube_migrate_014();
DROP FUNCTION minitube_migrate_014();
