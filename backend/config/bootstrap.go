package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
)

// EnsureConfig loads the config at path. If the file does not exist, it writes a
// default config (with a randomly generated panelAccessToken) to path and loads
// that instead. The bool result is true when a default config was created on
// disk, false when an existing file was loaded. Parse errors and other non-
// "not exist" failures are returned as-is and never trigger auto-creation — only
// a genuinely missing file is self-bootstrapped, mirroring how GetDBEncryptionKey
// auto-generates .master-key on first use.
func EnsureConfig(path string) (*Config, bool, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, false, nil
	}
	if !os.IsNotExist(err) {
		return nil, false, err
	}

	token, err := generateRandomToken()
	if err != nil {
		return nil, false, err
	}
	if err := writeDefaultConfig(path, token); err != nil {
		return nil, false, err
	}

	cfg, err = Load(path)
	if err != nil {
		return nil, false, err
	}
	return cfg, true, nil
}

// generateRandomToken returns a fresh random panel access token of the form
// "elysia-<43 url-safe base64 chars>" (32 random bytes). It is suitable as an
// unguessable first-run credential that the operator should rotate later.
func generateRandomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "elysia-" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// writeDefaultConfig writes a minimal bootstrap config to path, mirroring the
// field set of config.json.example but with the provided token in place of the
// insecure "change-me" placeholder. The file is written with 0o600 to protect the
// freshly generated credential.
func writeDefaultConfig(path, token string) error {
	defaults := map[string]any{
		"host":             "127.0.0.1",
		"port":             8765,
		"panelAccessToken": token,
		"databasePath":     "elysia-api.sqlite3",
		"logLevel":         "info",
		"httpTimeout":      120,
		"secretKeyPath":    ".master-key",
	}
	out, err := json.MarshalIndent(defaults, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}
