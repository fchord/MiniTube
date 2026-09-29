CREATE TABLE IF NOT EXISTS playback_client_reports (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id         text NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    user_id          uuid REFERENCES users (id) ON DELETE SET NULL,
    page             text NOT NULL DEFAULT '',
    os               text NOT NULL DEFAULT '',
    os_version       text NOT NULL DEFAULT '',
    browser          text NOT NULL DEFAULT '',
    browser_version  text NOT NULL DEFAULT '',
    hw_codecs        text[] NOT NULL DEFAULT '{}',
    user_agent       text NOT NULL DEFAULT '',
    payload          jsonb NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS playback_client_reports_created_idx
    ON playback_client_reports (created_at DESC);
CREATE INDEX IF NOT EXISTS playback_client_reports_os_browser_idx
    ON playback_client_reports (os, browser, created_at DESC);
