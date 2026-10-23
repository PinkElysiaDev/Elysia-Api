package server

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestWindowsCLIConsoleHookDisabled(t *testing.T) {
	if cobra.MousetrapHelpText != "" {
		t.Fatal("embedded CLI must not inspect Explorer or exit the server")
	}
}
