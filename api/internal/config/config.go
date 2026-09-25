package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv           string
	HTTPAddr         string
	DatabaseURL      string
	JWTSecret        []byte
	PublicBaseURL    string
	UploadDir        string
	RTMPBaseURL      string
	SRSHLSDir        string
	LiveHLSDir       string
	SRSHookSecret    string
	LiveIdleTTL      time.Duration
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	EmailTokenTTL    time.Duration
	AdminUserIDs     []string
	AdminPassword    string
	AdminSetupURL    string
	MediaEdgeTLSAddr string
	MediaEdgeTLSCert string
	MediaEdgeTLSKey  string
	MediaEdgeTLSHost string
}

func FromEnv() Config {
	return Config{
		AppEnv:           strings.ToLower(strings.TrimSpace(getenv("APP_ENV", "dev"))),
		HTTPAddr:         getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:      getenv("DATABASE_URL", "postgres://minitube:minitube@127.0.0.1:5433/minitube?sslmode=disable"),
		JWTSecret:        []byte(getenv("JWT_SECRET", "dev-change-me")),
		PublicBaseURL:    getenv("PUBLIC_BASE_URL", "http://127.0.0.1:8080"),
		UploadDir:        getenv("UPLOAD_DIR", "./data/uploads"),
		RTMPBaseURL:      getenv("RTMP_BASE_URL", "rtmp://127.0.0.1:1935/live"),
		SRSHLSDir:        getenv("SRS_HLS_DIR", "./data/srs-hls"),
		LiveHLSDir:       getenv("LIVE_HLS_DIR", "./data/live-hls"),
		SRSHookSecret:    getenv("SRS_HOOK_SECRET", "dev-srs-hook"),
		LiveIdleTTL:      duration("LIVE_IDLE_TTL", time.Hour),
		AccessTokenTTL:   duration("ACCESS_TOKEN_TTL", 7*24*time.Hour),
		RefreshTokenTTL:  duration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		EmailTokenTTL:    duration("EMAIL_TOKEN_TTL", 24*time.Hour),
		AdminUserIDs:     splitCSV(getenv("ADMIN_USER_IDS", "")),
		AdminPassword:    getenv("ADMIN_PASSWORD", ""),
		AdminSetupURL:    getenv("ADMIN_SETUP_URL", "http://192.168.43.111:8080/admin/setup"),
		MediaEdgeTLSAddr: getenv("MEDIA_EDGE_TLS_ADDR", ""),
		MediaEdgeTLSCert: getenv("MEDIA_EDGE_TLS_CERT", "/data/ctc-tls/fullchain.pem"),
		MediaEdgeTLSKey:  getenv("MEDIA_EDGE_TLS_KEY", "/data/ctc-tls/privkey.pem"),
		MediaEdgeTLSHost: getenv("MEDIA_EDGE_TLS_HOST", "ctc.minitube.19121122.xyz"),
	}
}

func (c Config) IsTest() bool {
	return c.AppEnv == "test"
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func duration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return fallback
}
