package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxAvatarBytes = 5 << 20
	MaxVideoBytes  = 5 << 30
	VideoChunkSize = 8 << 20
)

var imageMIME = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

var videoExt = map[string]string{
	"video/mp4":        ".mp4",
	"video/webm":       ".webm",
	"video/quicktime":  ".mov",
	"video/x-matroska": ".mkv",
	"video/mpeg":       ".mpg",
}

type pending struct {
	UserID    string
	ObjectKey string
	Expires   time.Time
	MaxBytes  int64
	AllowMIME map[string]string // nil = do not check Content-Type
}

type chunkSess struct {
	UserID  string
	VideoID string
	Ext     string
	Size    int64
	Expires time.Time
}

type Local struct {
	dir     string
	baseURL string
	mu      sync.Mutex
	uploads map[string]pending
	latest  map[string]string
	chunks  map[string]chunkSess
}

func NewLocal(dir, publicBaseURL string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Local{
		dir:     dir,
		baseURL: strings.TrimRight(publicBaseURL, "/"),
		uploads: map[string]pending{},
		latest:  map[string]string{},
		chunks:  map[string]chunkSess{},
	}, nil
}

func (l *Local) Dir() string { return l.dir }

func (l *Local) SetBaseURL(publicBaseURL string) {
	l.baseURL = strings.TrimRight(publicBaseURL, "/")
}

func (l *Local) URL(objectKey string) string {
	return l.baseURL + "/v1/media/" + strings.TrimPrefix(objectKey, "/")
}

func (l *Local) Abs(objectKey string) (string, error) {
	return l.safePath(objectKey)
}

func (l *Local) Exists(objectKey string) bool {
	p, err := l.safePath(objectKey)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func (l *Local) Wait(ctx context.Context, objectKey string, d time.Duration) bool {
	if l.Exists(objectKey) {
		return true
	}
	deadline := time.Now().Add(d)
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return l.Exists(objectKey)
		case <-t.C:
			if l.Exists(objectKey) {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
		}
	}
}

func (l *Local) BeginAvatar(userID, mimeType string) (uploadURL, objectKey string, headers map[string]string, err error) {
	ext, mimeType, err := matchMIME(imageMIME, mimeType)
	if err != nil {
		return "", "", nil, fmt.Errorf("unsupported image type")
	}
	return l.begin(userID, filepath.ToSlash(filepath.Join("avatars", userID)), ext, mimeType, maxAvatarBytes, 15*time.Minute, imageMIME)
}

func (l *Local) BeginPostImage(userID, mimeType string) (uploadURL, objectKey string, headers map[string]string, err error) {
	ext, mimeType, err := matchMIME(imageMIME, mimeType)
	if err != nil {
		return "", "", nil, fmt.Errorf("unsupported image type")
	}
	id, err := randomHex(8)
	if err != nil {
		return "", "", nil, err
	}
	return l.begin(userID, filepath.ToSlash(filepath.Join("posts", userID)), id+ext, mimeType, maxAvatarBytes, 15*time.Minute, imageMIME)
}

func (l *Local) KeyOwnedBy(userID, objectKey string) bool {
	prefix := filepath.ToSlash(filepath.Join("posts", userID)) + "/"
	key := filepath.ToSlash(objectKey)
	return strings.HasPrefix(key, prefix) && l.Exists(key)
}

func (l *Local) BeginVideo(userID, videoID, mimeType, filename string) (uploadURL, objectKey string, headers map[string]string, err error) {
	ext := extFromFilename(filename)
	if ext == "" {
		ext, mimeType, err = matchMIME(videoExt, mimeType)
		if err != nil {
			ext = ".mp4"
			if mimeType == "" {
				mimeType = "video/mp4"
			}
			err = nil
		}
	} else if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return l.begin(userID, filepath.ToSlash(filepath.Join("sources", videoID)), "source"+ext, mimeType, MaxVideoBytes, 2*time.Hour, nil)
}

func (l *Local) begin(userID, dir, name, mimeType string, max int64, ttl time.Duration, allow map[string]string) (string, string, map[string]string, error) {
	token, err := randomHex(16)
	if err != nil {
		return "", "", nil, err
	}
	objectKey := dir + "/" + name
	l.mu.Lock()
	l.uploads[token] = pending{UserID: userID, ObjectKey: objectKey, Expires: time.Now().Add(ttl), MaxBytes: max, AllowMIME: allow}
	l.latest[userID] = objectKey
	l.mu.Unlock()
	headers := map[string]string{}
	if mimeType != "" {
		headers["Content-Type"] = mimeType
	}
	return l.baseURL + "/v1/uploads/" + token, objectKey, headers, nil
}

func (l *Local) Put(token string, r io.Reader, contentType string) error {
	l.mu.Lock()
	p, ok := l.uploads[token]
	l.mu.Unlock()
	if !ok || time.Now().After(p.Expires) {
		return errNotFound
	}
	if p.AllowMIME != nil && contentType != "" {
		if _, _, err := matchMIME(p.AllowMIME, contentType); err != nil {
			return fmt.Errorf("unsupported type")
		}
	}
	dest, err := l.safePath(p.ObjectKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, p.MaxBytes+1))
	cerr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return cerr
	}
	if n > p.MaxBytes {
		_ = os.Remove(tmp)
		return fmt.Errorf("file too large")
	}
	return os.Rename(tmp, dest)
}

func (l *Local) CompleteAvatar(userID string) (publicURL string, err error) {
	l.mu.Lock()
	key := l.latest[userID]
	l.mu.Unlock()
	if key == "" || !l.Exists(key) {
		return "", fmt.Errorf("no pending upload")
	}
	return l.URL(key), nil
}

func (l *Local) Serve(w http.ResponseWriter, r *http.Request, objectKey string) {
	path, err := l.safePath(objectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	case ".ts":
		w.Header().Set("Content-Type", "video/mp2t")
	case ".vtt":
		w.Header().Set("Content-Type", "text/vtt")
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeFile(w, r, path)
}

func (l *Local) RemovePrefix(prefix string) {
	p, err := l.safePath(prefix)
	if err != nil {
		return
	}
	_ = os.RemoveAll(p)
}

func (l *Local) FileSize(objectKey string) (int64, bool) {
	p, err := l.safePath(objectKey)
	if err != nil {
		return 0, false
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return 0, false
	}
	return st.Size(), true
}

func (l *Local) safePath(objectKey string) (string, error) {
	clean := filepath.Clean("/" + objectKey)
	clean = strings.TrimPrefix(clean, "/")
	full := filepath.Join(l.dir, clean)
	rel, err := filepath.Rel(l.dir, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errNotFound
	}
	return full, nil
}

func matchMIME(allowed map[string]string, mimeType string) (ext, normalized string, err error) {
	normalized = mimeType
	ext, ok := allowed[mimeType]
	if !ok {
		if parsed, _, perr := mime.ParseMediaType(mimeType); perr == nil {
			ext, ok = allowed[parsed]
			normalized = parsed
		}
	}
	if !ok {
		return "", "", fmt.Errorf("unsupported type")
	}
	return ext, normalized, nil
}

func extFromFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mp4", ".webm", ".mov", ".mkv", ".mpg", ".mpeg":
		return ext
	default:
		return ""
	}
}

var errNotFound = errors.New("not found")

func IsNotFound(err error) bool {
	return errors.Is(err, errNotFound)
}

func (l *Local) BeginVideoChunks(userID, videoID, mimeType, filename string, size int64) error {
	if size < 1 || size > MaxVideoBytes {
		return fmt.Errorf("file too large")
	}
	ext := extFromFilename(filename)
	if ext == "" {
		got, _, err := matchMIME(videoExt, mimeType)
		if err != nil || got == "" {
			ext = ".mp4"
		} else {
			ext = got
		}
	}
	dir, err := l.safePath(filepath.ToSlash(filepath.Join("tmp", videoID, "chunks")))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	l.mu.Lock()
	l.chunks[videoID] = chunkSess{
		UserID: userID, VideoID: videoID, Ext: ext, Size: size,
		Expires: time.Now().Add(2 * time.Hour),
	}
	l.mu.Unlock()
	return nil
}

func (l *Local) PutChunk(userID, videoID string, index int, r io.Reader) error {
	l.mu.Lock()
	s, ok := l.chunks[videoID]
	l.mu.Unlock()
	if !ok || s.UserID != userID || time.Now().After(s.Expires) {
		return errNotFound
	}
	nchunks := int((s.Size + VideoChunkSize - 1) / VideoChunkSize)
	if nchunks < 1 {
		nchunks = 1
	}
	if index < 0 || index >= nchunks {
		return fmt.Errorf("invalid chunk index")
	}
	dest, err := l.safePath(filepath.ToSlash(filepath.Join("tmp", videoID, "chunks", fmt.Sprintf("%06d", index))))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, VideoChunkSize+1))
	cerr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return cerr
	}
	if n > VideoChunkSize {
		_ = os.Remove(tmp)
		return fmt.Errorf("chunk too large")
	}
	last := index == nchunks-1
	if last {
		want := s.Size - int64(index)*VideoChunkSize
		if n != want {
			_ = os.Remove(tmp)
			return fmt.Errorf("chunk size mismatch")
		}
	} else if n != VideoChunkSize {
		_ = os.Remove(tmp)
		return fmt.Errorf("chunk size mismatch")
	}
	return os.Rename(tmp, dest)
}

func (l *Local) MergeVideo(videoID string) (string, error) {
	l.mu.Lock()
	s, ok := l.chunks[videoID]
	l.mu.Unlock()
	if !ok {
		return "", errNotFound
	}
	chunkDir, err := l.safePath(filepath.ToSlash(filepath.Join("tmp", videoID, "chunks")))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(chunkDir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		names = append(names, e.Name())
	}
	nchunks := int((s.Size + VideoChunkSize - 1) / VideoChunkSize)
	if len(names) != nchunks {
		return "", fmt.Errorf("missing chunks")
	}
	outKey := filepath.ToSlash(filepath.Join("tmp", videoID, "source"+s.Ext))
	outPath, err := l.safePath(outKey)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	var total int64
	for i := 0; i < nchunks; i++ {
		part := filepath.Join(chunkDir, fmt.Sprintf("%06d", i))
		f, err := os.Open(part)
		if err != nil {
			_ = out.Close()
			_ = os.Remove(outPath)
			return "", fmt.Errorf("missing chunks")
		}
		n, err := io.Copy(out, f)
		_ = f.Close()
		if err != nil {
			_ = out.Close()
			_ = os.Remove(outPath)
			return "", err
		}
		total += n
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(outPath)
		return "", err
	}
	if total != s.Size {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("size mismatch")
	}
	return outKey, nil
}

func (l *Local) AbortVideoTmp(videoID string) {
	l.mu.Lock()
	delete(l.chunks, videoID)
	l.mu.Unlock()
	l.RemovePrefix(filepath.ToSlash(filepath.Join("tmp", videoID)))
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
