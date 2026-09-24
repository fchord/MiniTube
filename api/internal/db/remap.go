package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func RemapVideoMedia(ctx context.Context, pool *pgxpool.Pool, uploadDir string) error {
	if strings.TrimSpace(uploadDir) == "" {
		return nil
	}
	rows, err := pool.Query(ctx, `SELECT old_id::text, video_id FROM video_id_legacy`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var oldID, newID string
		if err := rows.Scan(&oldID, &newID); err != nil {
			return err
		}
		for _, kind := range []string{"hls", "sources", "tmp"} {
			if err := remapLeaf(uploadDir, kind, oldID, newID); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func remapLeaf(root, kind, oldID, newID string) error {
	if oldID == "" || newID == "" || oldID == newID {
		return nil
	}
	oldPath := filepath.Join(root, kind, oldID)
	newPath := filepath.Join(root, kind, newID)
	st, err := os.Lstat(oldPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if _, err := os.Stat(newPath); err == nil {
		if err := os.RemoveAll(oldPath); err != nil {
			return err
		}
	} else if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	return os.Symlink(newID, oldPath)
}
