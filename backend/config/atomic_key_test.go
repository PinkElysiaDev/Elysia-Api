package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDBEncryptionKeyAtomicPersistence(t *testing.T) {
	t.Setenv("ELYSIA_API_MASTER_KEY", "")
	path := filepath.Join(t.TempDir(), "key")
	// An empty pre-existing key file is regenerated, with private permissions.
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{SecretKeyPath: path}
	key := cfg.GetDBEncryptionKey()
	if len(key) == 0 {
		t.Fatal("key was not generated")
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(key, stored) || !bytes.Equal(key, cfg.GetDBEncryptionKey()) {
		t.Fatalf("key not persisted consistently: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("key mode %o", info.Mode().Perm())
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("staging files leaked: %v %v", files, err)
	}
	cfg.SecretKeyPath = filepath.Join(t.TempDir(), "missing", "key")
	if key := cfg.GetDBEncryptionKey(); key != nil {
		t.Fatal("enabled persistent encryption with an unwritten key")
	}
}
