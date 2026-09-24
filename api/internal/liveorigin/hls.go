package liveorigin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"minitube/api/internal/store"
)

var errBadHLSPath = errors.New("bad hls path")

func (e *Engine) ArchiveDir(ingestKey string) string {
	return filepath.Join(e.Cfg.LiveHLSDir, ingestKey)
}

func (e *Engine) ArchiveFile(ingestKey, rest string) (string, error) {
	if ingestKey == "" || strings.Contains(ingestKey, "..") || strings.ContainsAny(ingestKey, `/\`) {
		return "", errBadHLSPath
	}
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" || strings.HasSuffix(strings.ToLower(rest), ".m3u8") {
		rest = "index.m3u8"
	} else {
		rest = path.Base(rest)
		if !strings.HasSuffix(strings.ToLower(rest), ".ts") || strings.Contains(rest, "..") {
			return "", errBadHLSPath
		}
	}
	full := filepath.Join(e.ArchiveDir(ingestKey), rest)
	root, err := filepath.Abs(e.ArchiveDir(ingestKey))
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	sep := string(os.PathSeparator)
	if abs != root && !strings.HasPrefix(abs, root+sep) {
		return "", errBadHLSPath
	}
	return abs, nil
}

func (e *Engine) HasArchive(ingestKey string) bool {
	p, err := e.ArchiveFile(ingestKey, "index.m3u8")
	if err != nil {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (e *Engine) ArchiveSize(ingestKey string) int64 {
	if ingestKey == "" {
		return 0
	}
	root := e.ArchiveDir(ingestKey)
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

func (e *Engine) srsHostFile(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" || strings.Contains(p, "..") {
		return "", errBadHLSPath
	}
	rel := p
	switch {
	case strings.HasPrefix(p, "/data/srs-hls/"):
		rel = strings.TrimPrefix(p, "/data/srs-hls/")
	case p == "/data/srs-hls":
		rel = ""
	case strings.HasPrefix(p, "./"):
		rel = path.Base(path.Dir(p)) + "/" + path.Base(p)
	case !strings.HasPrefix(p, "/"):
		rel = p
	default:
		return "", errBadHLSPath
	}
	rel = strings.TrimPrefix(rel, "/")
	full := filepath.Join(e.Cfg.SRSHLSDir, filepath.FromSlash(rel))
	root, err := filepath.Abs(e.Cfg.SRSHLSDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	sep := string(os.PathSeparator)
	if abs != root && !strings.HasPrefix(abs, root+sep) {
		return "", errBadHLSPath
	}
	return abs, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func (e *Engine) OnHLS(_ context.Context, stream, file, m3u8 string) error {
	if stream == "" {
		return errBadHLSPath
	}
	dir := e.ArchiveDir(stream)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if file != "" {
		src, err := e.srsHostFile(file)
		if err != nil {
			return err
		}
		if err := copyFile(src, filepath.Join(dir, filepath.Base(src))); err != nil {
			return err
		}
	}
	if m3u8 != "" {
		src, err := e.srsHostFile(m3u8)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, "index.m3u8")
		if err := copyFile(src, dst); err != nil {
			return err
		}
		body, err := os.ReadFile(dst)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, RewritePlaylist(body, false), 0o644); err != nil {
			return err
		}
	}
	e.maybeThumb(stream)
	return nil
}

func (e *Engine) EnsureEndList(ingestKey string) error {
	p, err := e.ArchiveFile(ingestKey, "index.m3u8")
	if err != nil {
		return err
	}
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(p, RewritePlaylist(body, true), 0o644)
}

func unpublishedLongEnough(live store.Live) bool {
	if live.Status == "ended" {
		return true
	}
	if live.UnpublishedAt == nil {
		return false
	}
	return time.Since(*live.UnpublishedAt) >= 2*time.Second
}

func (e *Engine) ServeHLS(w http.ResponseWriter, r *http.Request, live store.Live, rest string) {
	p, err := e.ArchiveFile(live.IngestKey, rest)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if e.Store != nil && live.Status == "live" {
		_ = e.Store.TouchLiveHLS(r.Context(), live.ID)
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if strings.HasSuffix(strings.ToLower(p), ".m3u8") {
		body, err := os.ReadFile(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body = RewritePlaylist(body, unpublishedLongEnough(live))
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
		w.Header().Set("CDN-Cache-Control", "no-store")
		w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "public, max-age=60")
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
}
