// Package atomicfile publishes complete files using a platform-aware library.
// It syncs file contents before replacement, but does not sync the parent
// directory: success is not a guarantee of rename durability after power loss.
package atomicfile

import (
	"errors"
	"os"

	library "github.com/marwatk/atomicfile"
)

// WriteFile stages data in the destination directory and replaces path only
// after writing, setting the requested mode and syncing the file. The library
// owns unique temporary names, cleanup and platform-specific replacement.
// POSIX permissions are explicit (not inherited from the destination or umask).
// On Windows, os.Chmod has its usual limited read-only-bit semantics.
func WriteFile(path string, data []byte, mode os.FileMode) (err error) {
	file, err := library.New(path, mode)
	if err != nil {
		return err
	}
	// New's mode parameter is not applied by v1.0.2. Apply it explicitly and
	// sync it together with the contents; the library's Close does not Sync.
	closing := false
	defer func() {
		if !closing {
			err = errors.Join(err, file.Abort())
		}
	}()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Chmod(mode); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	// Close owns cleanup on close/replace errors. Do not Abort after it: the
	// old temporary name might have been reused by another writer.
	closing = true
	return file.Close()
}

// ReplaceFile publishes an already prepared file on the same filesystem.
// The caller owns validation, Sync/Close and cleanup on failure. This is used
// for SQLite snapshots, whose contents must be checked before publication.
func ReplaceFile(source, destination string) error {
	return library.ReplaceFile(source, destination)
}
