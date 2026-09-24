CREATE TABLE IF NOT EXISTS site_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO site_settings (key, value) VALUES ('shorts_engine', 'legacy')
ON CONFLICT (key) DO NOTHING;
