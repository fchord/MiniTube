CREATE TABLE IF NOT EXISTS transcode_workers (
    id               text PRIMARY KEY,
    rank             integer NOT NULL CHECK (rank >= 1),
    encoder          text NOT NULL,
    busy             boolean NOT NULL DEFAULT false,
    healthy          boolean NOT NULL DEFAULT true,
    last_seen        timestamptz NOT NULL DEFAULT now(),
    claimed_video_id uuid REFERENCES videos (id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS transcode_workers_rank_idx
    ON transcode_workers (rank, healthy, busy, last_seen);
