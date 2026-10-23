package server

import "github.com/spf13/cobra"

func init() {
	// Commands run inside the HTTP server, never as an Explorer-launched CLI.
	cobra.MousetrapHelpText = ""
}
