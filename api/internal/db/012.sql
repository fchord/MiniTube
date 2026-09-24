INSERT INTO site_settings (key, value) VALUES ('media_edge_enabled', 'off')
ON CONFLICT (key) DO NOTHING;
INSERT INTO site_settings (key, value) VALUES ('media_edges', '[]')
ON CONFLICT (key) DO NOTHING;
