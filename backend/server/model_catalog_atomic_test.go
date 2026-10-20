package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModelCatalogCacheAtomicFailureAndPrivateStaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	// The old fixed .tmp pathname could clobber an unrelated file or collide
	// with another writer. The library must choose its own exclusive name.
	if err := os.WriteFile(path+".tmp", []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog := &modelCatalog{cachePathGetter: func() string { return path }, count: 7}
	catalog.saveToCache([]byte(`{"source":{"models":{}}}`), time.Now())
	if catalog.lastError != "" {
		t.Fatal(catalog.lastError)
	}
	data, err := os.ReadFile(path + ".tmp")
	if err != nil || string(data) != "keep" {
		t.Fatalf("clobbered fixed staging file: %q %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	catalog.saveToCache([]byte(`{}`), time.Now())
	if !strings.Contains(catalog.lastError, "cache write:") || catalog.count != 7 {
		t.Fatalf("rename failure not reported or memory changed: %+v", catalog)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("temporary files survived failure: %v %v", files, err)
	}
}
