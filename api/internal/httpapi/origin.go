package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

func requestOrigin(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := r.Host
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); proto == "http" || proto == "https" {
		scheme = proto
	}
	return scheme + "://" + host
}

func hostnameOnly(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]"); i > 1 {
			return host[1:i]
		}
	}
	if strings.Count(host, ":") == 1 {
		h, _, _ := strings.Cut(host, ":")
		return h
	}
	return host
}

func sameOriginMediaURL(r *http.Request, abs string, keepHosts []string) string {
	if abs == "" || r == nil {
		return abs
	}
	u, err := url.Parse(abs)
	if err != nil || u.Path == "" {
		return abs
	}
	if !strings.HasPrefix(u.Path, "/v1/media/") && !strings.HasPrefix(u.Path, "/v1/live/") {
		return abs
	}
	mediaHost := hostnameOnly(u.Host)
	for _, h := range keepHosts {
		if mediaHost != "" && mediaHost == hostnameOnly(h) {
			return abs
		}
	}
	origin := requestOrigin(r)
	if origin == "" {
		return abs
	}
	base, err := url.Parse(origin)
	if err != nil || base.Host == "" {
		return abs
	}
	u.Scheme = base.Scheme
	u.Host = base.Host
	u.User = nil
	return u.String()
}

func rewriteMediaField(r *http.Request, m map[string]any, key string, keepHosts []string) {
	if m == nil {
		return
	}
	s, ok := m[key].(string)
	if !ok {
		return
	}
	m[key] = sameOriginMediaURL(r, s, keepHosts)
}

func rewriteMediaURLList(r *http.Request, items []map[string]any, key string, keepHosts []string) {
	for _, it := range items {
		rewriteMediaField(r, it, key, keepHosts)
	}
}
