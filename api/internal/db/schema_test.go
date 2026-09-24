package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedSchemaMatchesDocs(t *testing.T) {
	docs, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if string(docs) != string(embedded) {
		t.Fatal("api/internal/db/schema.sql drifted from docs/schema.sql")
	}
}
