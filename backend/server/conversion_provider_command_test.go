package server

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConversionProviderSelectionIsIsolated(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "provider.test")
	t.Setenv("ELYSIA_CONVERSION_HTTP_PROVIDER_BINARY", binary)
	t.Setenv("ELYSIA_CONVERSION_HTTP_PROVIDER_CWD", directory)
	t.Setenv("ELYSIA_CONVERSION_HTTP_PROVIDER_EXECUTION", "packages")
	for _, role := range []string{"provider", "gateway"} {
		command := exec.Command("gateway.test", "-test.run=^TestConversionHTTPProcess$")
		command.Env = []string{"ELYSIA_CONVERSION_HTTP_EXECUTION=legacy"}
		configureConversionProvider(command, role)
		if role == "gateway" {
			if command.Path != "gateway.test" || command.Dir != "" || len(command.Env) != 1 {
				t.Fatal("provider selection changed the measured gateway", command)
			}
		} else if command.Path != binary || command.Args[0] != binary || command.Dir != directory || command.Env[len(command.Env)-1] != "ELYSIA_CONVERSION_HTTP_EXECUTION=packages" {
			t.Fatal("provider did not use the fixed binary, fixtures and execution", command)
		}
	}
}
