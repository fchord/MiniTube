-- MiniTube PostgreSQL schema.
-- Canonical: docs/schema.sql (duplicated at api/internal/db/schema.sql for go:embed).
-- 本文件是迁移 001 基线：多数主键 UUID，时间 timestamptz UTC。
-- 014 起 videos.id 改为 10 位 [A-Za-z0-9]，旧 UUID 在 video_id_legacy；评论/点赞/收藏里
-- 指向视频的 target_id 同步为 text。见 api/internal/db/014.sql。

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

-- ---------------------------------------------------------------------------
-- 枚举
-- ---------------------------------------------------------------------------

CREATE TYPE user_status AS ENUM ('active', 'disabled', 'deleted');
CREATE TYPE channel_visibility AS ENUM ('public', 'unlisted', 'private');
CREATE TYPE video_kind AS ENUM ('long', 'short');
CREATE TYPE video_status AS ENUM (
    'draft',
    'uploading',
    'processing',
    'ready',
    'failed',
    'unpublished'
);
CREATE TYPE video_visibility AS ENUM ('public', 'private', 'deleted');
CREATE TYPE live_status AS ENUM ('scheduled', 'live', 'ended');
CREATE TYPE comment_target AS ENUM ('video', 'post', 'live');
CREATE TYPE like_target AS ENUM ('video', 'post', 'comment', 'live');
CREATE TYPE favorite_target AS ENUM ('video', 'post', 'live');
CREATE TYPE favorite_list_kind AS ENUM ('watch_later', 'custom');

-- ---------------------------------------------------------------------------
-- 用户
-- ---------------------------------------------------------------------------

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username        citext NOT NULL UNIQUE,
    email           citext NOT NULL UNIQUE,
    email_verified_at timestamptz,
    password_hash   text NOT NULL,
    nickname        text NOT NULL DEFAULT '',
    avatar_url      text,
    bio             text NOT NULL DEFAULT '',
    status          user_status NOT NULL DEFAULT 'active',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_username_len CHECK (char_length(username::text) BETWEEN 3 AND 32),
    CONSTRAINT users_nickname_len CHECK (char_length(nickname) BETWEEN 0 AND 64)
);

CREATE TABLE email_verification_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_hash    text NOT NULL UNIQUE,
    user_agent      text,
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);

-- ---------------------------------------------------------------------------
-- 频道（订阅对象）
-- ---------------------------------------------------------------------------

CREATE TABLE channels (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id   uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name            text NOT NULL,
    handle          citext NOT NULL UNIQUE,
    avatar_url      text,
    banner_url      text,
    description     text NOT NULL DEFAULT '',
    tags            text[] NOT NULL DEFAULT '{}',
    visibility      channel_visibility NOT NULL DEFAULT 'public',
    is_default      boolean NOT NULL DEFAULT false,
    subscriber_count bigint NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT channels_name_len CHECK (char_length(name) BETWEEN 1 AND 64),
    CONSTRAINT channels_handle_len CHECK (char_length(handle::text) BETWEEN 3 AND 32)
);

-- 每用户至多一个默认频道
CREATE UNIQUE INDEX channels_one_default_per_user
    ON channels (owner_user_id)
    WHERE is_default;

CREATE INDEX channels_owner_user_id_idx ON channels (owner_user_id);

CREATE TABLE subscriptions (
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel_id  uuid NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel_id)
);

CREATE INDEX subscriptions_channel_id_idx ON subscriptions (channel_id);

-- ---------------------------------------------------------------------------
-- 点播视频（long / short 同行）
-- ---------------------------------------------------------------------------

CREATE TABLE videos (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      uuid NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    kind            video_kind NOT NULL DEFAULT 'long',
    title           text NOT NULL,
    description     text NOT NULL DEFAULT '',
    status          video_status NOT NULL DEFAULT 'draft',
    visibility      video_visibility NOT NULL DEFAULT 'public',
    duration_ms     integer,
    width           integer,
    height          integer,
    -- 呈现提示，禁止用它当 kind 约束
    aspect_ratio    numeric,
    thumbnail_url   text,
    preview_sprite_url text,
    source_object_key text,
    source_bytes    bigint,
    transcode_claimed_at timestamptz,
    error_message   text,
    view_count      bigint NOT NULL DEFAULT 0,
    like_count      bigint NOT NULL DEFAULT 0,
    comment_count   bigint NOT NULL DEFAULT 0,
    published_at    timestamptz,
    scheduled_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT videos_title_len CHECK (char_length(title) BETWEEN 1 AND 200)
);

CREATE INDEX videos_channel_id_kind_idx ON videos (channel_id, kind, published_at DESC);
CREATE INDEX videos_status_published_idx ON videos (status, published_at DESC)
    WHERE status = 'ready';

-- 视频档位与音轨解耦：视频 HLS 不含（或仅默认）音频
CREATE TABLE video_renditions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id        uuid NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    height          integer NOT NULL,
    bandwidth_bps   integer NOT NULL,
    playlist_key    text NOT NULL,
    codec           text NOT NULL DEFAULT 'avc1',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (video_id, height)
);

CREATE TABLE audio_tracks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id        uuid NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    language        text NOT NULL,
    label           text NOT NULL,
    is_default      boolean NOT NULL DEFAULT false,
    playlist_key    text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (video_id, language, label)
);

CREATE UNIQUE INDEX audio_tracks_one_default_per_video
    ON audio_tracks (video_id)
    WHERE is_default;

CREATE TABLE subtitle_tracks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id        uuid NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    language        text NOT NULL,
    label           text NOT NULL,
    is_default      boolean NOT NULL DEFAULT false,
    vtt_key         text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (video_id, language, label)
);

-- ---------------------------------------------------------------------------
-- 直播（Phase 5 开放入口；表先建）
-- ---------------------------------------------------------------------------

CREATE TABLE live_streams (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      uuid NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    title           text NOT NULL,
    description     text NOT NULL DEFAULT '',
    status          live_status NOT NULL DEFAULT 'scheduled',
    visibility      video_visibility NOT NULL DEFAULT 'public',
    ingest_key_hash text NOT NULL,
    ingest_key      text,
    playback_url    text,
    thumbnail_url   text,
    scheduled_at    timestamptz,
    started_at      timestamptz,
    ended_at        timestamptz,
    vod_video_id    uuid REFERENCES videos (id) ON DELETE SET NULL,
    archive_bytes   bigint,
    viewer_count    integer NOT NULL DEFAULT 0,
    like_count      bigint NOT NULL DEFAULT 0,
    unpublished_at  timestamptz,
    last_hls_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX live_streams_channel_id_idx ON live_streams (channel_id, status);
CREATE UNIQUE INDEX live_streams_ingest_key_uidx ON live_streams (ingest_key)
    WHERE ingest_key IS NOT NULL AND ingest_key <> '';

-- ---------------------------------------------------------------------------
-- 社交帖（Phase 3）
-- ---------------------------------------------------------------------------

CREATE TABLE posts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel_id          uuid REFERENCES channels (id) ON DELETE SET NULL,
    body                text NOT NULL,
    mentioned_video_id  uuid REFERENCES videos (id) ON DELETE SET NULL,
    like_count          bigint NOT NULL DEFAULT 0,
    comment_count       bigint NOT NULL DEFAULT 0,
    view_count          bigint NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT posts_body_len CHECK (char_length(body) BETWEEN 1 AND 2000)
);

CREATE INDEX posts_author_idx ON posts (author_user_id, created_at DESC);
CREATE INDEX posts_channel_idx ON posts (channel_id, created_at DESC)
    WHERE channel_id IS NOT NULL;

CREATE TABLE post_images (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id     uuid NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    object_key  text NOT NULL,
    width       integer,
    height      integer,
    sort_order  smallint NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX post_images_post_id_idx ON post_images (post_id, sort_order);

-- ---------------------------------------------------------------------------
-- 评论（点播/帖子 HTTP；直播走实时通道但同表落库）
-- ---------------------------------------------------------------------------

CREATE TABLE comments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_type     comment_target NOT NULL,
    target_id       uuid NOT NULL,
    parent_id       uuid REFERENCES comments (id) ON DELETE CASCADE,
    body            text NOT NULL,
    like_count      bigint NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,
    CONSTRAINT comments_body_len CHECK (char_length(body) BETWEEN 1 AND 2000)
);

CREATE INDEX comments_target_idx ON comments (target_type, target_id, created_at);
CREATE INDEX comments_user_id_idx ON comments (user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- 点赞 / 收藏 / 观看记录
-- ---------------------------------------------------------------------------

CREATE TABLE likes (
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_type like_target NOT NULL,
    target_id   uuid NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, target_type, target_id)
);

CREATE INDEX likes_target_idx ON likes (target_type, target_id);

CREATE TABLE favorite_lists (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        favorite_list_kind NOT NULL DEFAULT 'custom',
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX favorite_lists_one_watch_later
    ON favorite_lists (user_id)
    WHERE kind = 'watch_later';

CREATE TABLE favorite_items (
    list_id     uuid NOT NULL REFERENCES favorite_lists (id) ON DELETE CASCADE,
    target_type favorite_target NOT NULL,
    target_id   uuid NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (list_id, target_type, target_id)
);

CREATE TABLE watch_history (
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    video_id        uuid NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    position_ms     integer NOT NULL DEFAULT 0,
    duration_ms     integer,
    completed       boolean NOT NULL DEFAULT false,
    last_watched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);

CREATE INDEX watch_history_user_recent_idx
    ON watch_history (user_id, last_watched_at DESC);

-- ---------------------------------------------------------------------------
-- 转码 worker 心跳（k8s 硬编瀑布：rank 越小越优先）
-- ---------------------------------------------------------------------------

CREATE TABLE transcode_workers (
    id               text PRIMARY KEY,
    rank             integer NOT NULL CHECK (rank >= 1),
    encoder          text NOT NULL,
    busy             boolean NOT NULL DEFAULT false,
    healthy          boolean NOT NULL DEFAULT true,
    last_seen        timestamptz NOT NULL DEFAULT now(),
    claimed_video_id uuid REFERENCES videos (id) ON DELETE SET NULL
);

CREATE INDEX transcode_workers_rank_idx
    ON transcode_workers (rank, healthy, busy, last_seen);

-- ---------------------------------------------------------------------------
-- 站点开关（改完立刻生效；/admin 口令+TOTP 可改）
-- ---------------------------------------------------------------------------

CREATE TABLE site_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO site_settings (key, value) VALUES ('shorts_engine', 'legacy');
-- 码流边缘分流，约定见 media-edges.md；缺省关闭，媒体仍走页面源 / Cloudflare
INSERT INTO site_settings (key, value) VALUES ('media_edge_enabled', 'off');
INSERT INTO site_settings (key, value) VALUES ('media_edges', '[]');

CREATE TABLE admin_account (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    password_hash TEXT NOT NULL,
    totp_secret TEXT NOT NULL DEFAULT '',
    totp_pending TEXT NOT NULL DEFAULT '',
    totp_enrolled BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- 注册后创建默认频道 + 稍后再看 的约定（由 API 事务保证，不在此写触发器）
-- ---------------------------------------------------------------------------
-- INSERT users
--   → INSERT channels (is_default = true, handle 由 username 派生)
--   → INSERT favorite_lists (kind = 'watch_later', name = '稍后再看')
