package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotCleanupKeepsReferencedFile(t *testing.T) {
	store := newJobTestStore(t, filepath.Join(t.TempDir(), "snapshot.sqlite"))
	snapshot, err := store.EnsureProtocolSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.removeUnreferencedSnapshot(snapshot.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot.Path); err != nil {
		t.Fatalf("referenced snapshot removed: %v", err)
	}
}

func TestSnapshotCommitFailureCleansOwnedFile(t *testing.T) {
	store := newJobTestStore(t, filepath.Join(t.TempDir(), "snapshot.sqlite"))
	_, err := store.db.Exec("CREATE TRIGGER reject_review_snapshot BEFORE INSERT ON settings WHEN NEW.key='protocol_current_snapshot_v2' BEGIN SELECT RAISE(ABORT,'synthetic settings write failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.EnsureProtocolSnapshot(t.Context())
	files, _ := filepath.Glob(store.path + ".protocol-snapshot-v2-*")
	if err == nil || len(files) != 0 {
		t.Fatalf("snapshot cleanup failed: err=%v files=%d", err, len(files))
	}
	t.Log("failed settings commit cleaned its own snapshot")
}
