package storage

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// sqliteDatabaseDSN escapes filesystem paths and applies connection-local
// settings on every open, including replacements after interrupted queries.
// Database-wide journal/auto-vacuum settings follow schema validation.
func sqliteDatabaseDSN(path string, options url.Values) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	filePath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	options.Set("_busy_timeout", strconv.FormatInt(sqliteBusyTimeout.Milliseconds(), 10))
	options.Set("_foreign_keys", "on")
	options.Add("_pragma", "temp_store(MEMORY)")
	u := url.URL{Scheme: "file", Path: filePath, RawQuery: options.Encode()}
	return u.String(), nil
}
