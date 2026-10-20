// Package dbpath defines filesystem rules shared by configuration and storage.
// It has no database driver or application dependencies.
package dbpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func DefaultLogPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + "-logs" + ext
}

// Validate resolves symlinked parents and existing files before either database
// is opened. SameFile also catches hard links.
func Validate(business, logs string) error {
	a, err := canonical(business)
	if err != nil {
		return err
	}
	b, err := canonical(logs)
	if err != nil {
		return err
	}
	x, xe := os.Stat(a)
	y, ye := os.Stat(b)
	if a == b || (xe == nil && ye == nil && os.SameFile(x, y)) {
		return errors.New("business and log database must be different files")
	}
	return nil
}

func canonical(path string) (string, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return "", errors.New("database paths must be filesystem paths")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		tail = append(tail, filepath.Base(path))
		path = filepath.Dir(path)
	}
}
