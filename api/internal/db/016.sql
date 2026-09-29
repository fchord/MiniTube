ALTER TABLE video_renditions DROP CONSTRAINT IF EXISTS video_renditions_video_id_height_key;
ALTER TABLE video_renditions DROP CONSTRAINT IF EXISTS video_renditions_video_id_height_codec_key;
ALTER TABLE video_renditions
    ADD CONSTRAINT video_renditions_video_id_height_codec_key UNIQUE (video_id, height, codec);
