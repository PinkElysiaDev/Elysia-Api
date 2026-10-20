package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileExplicitPermissionsAndCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, tc := range []struct {
		data string
		mode os.FileMode
	}{{"first", 0640}, {"replacement", 0600}} {
		if err := WriteFile(path, []byte(tc.data), tc.mode); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != tc.data {
			t.Fatalf("contents %q: %v", data, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != tc.mode {
			t.Fatalf("mode %o want %o", info.Mode().Perm(), tc.mode)
		}
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary files survived commit: %v %v", files, err)
	}
}

func TestWriteFileFailureRetainsDestinationAndCleansTemporary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "destination")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(path, "keep")
	if err := os.WriteFile(keep, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0600); err == nil {
		t.Fatal("replaced a nonempty directory")
	}
	data, err := os.ReadFile(keep)
	if err != nil || string(data) != "old" {
		t.Fatalf("old destination changed: %q %v", data, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary files survived failure: %v %v", files, err)
	}
	err = WriteFile(filepath.Join(dir, "missing", "file"), []byte("new"), 0600)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost filesystem cause: %v", err)
	}
}

func TestWriteFileReplacesSymlinkEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires Windows privileges")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(link, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatalf("followed link: %q %v", data, err)
	}
	info, err := os.Lstat(link)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("link not replaced: %v %v", info, err)
	}
}
