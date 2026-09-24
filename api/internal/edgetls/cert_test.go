package edgetls

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureWritesCert(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "fullchain.pem")
	key := filepath.Join(dir, "privkey.pem")
	if err := Ensure(cert, key, "ctc.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cert); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(cert, key, "ctc.example"); err != nil {
		t.Fatal(err)
	}
}
