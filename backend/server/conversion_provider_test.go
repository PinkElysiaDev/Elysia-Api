package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bytedance/sonic"
)

// A/B/C can share one simulator binary and its captured package sources while
// each gateway continues to execute its own measured code and configuration.
func configureConversionProvider(command *exec.Cmd, role string) {
	binary := os.Getenv("ELYSIA_CONVERSION_HTTP_PROVIDER_BINARY")
	if role != "provider" || binary == "" {
		return
	}
	command.Path, command.Args[0] = binary, binary
	command.Dir = os.Getenv("ELYSIA_CONVERSION_HTTP_PROVIDER_CWD")
	command.Env = append(command.Env, "ELYSIA_CONVERSION_HTTP_EXECUTION="+os.Getenv("ELYSIA_CONVERSION_HTTP_PROVIDER_EXECUTION"))
}

func writeConversionProviderIdentity(t *testing.T, directory string, provider conversionHTTPAddress) {
	t.Helper()
	if os.Getenv("ELYSIA_CONVERSION_HTTP_PROVIDER_BINARY") == "" {
		return
	}
	proof := provider.Execution
	if proof == nil || proof.Commit != os.Getenv("ELYSIA_CONVERSION_HTTP_PROVIDER_COMMIT") || proof.Execution != os.Getenv("ELYSIA_CONVERSION_HTTP_PROVIDER_EXECUTION") || proof.Compiler == "" {
		t.Fatal("fixed provider execution identity does not match its recorded build")
	}
	if target := os.Getenv("ELYSIA_FLAME_TARGET"); target != "" && proof.Packages[target] == "" {
		t.Fatal("fixed provider did not report the measured target package")
	}
	raw, err := sonic.ConfigStd.MarshalIndent(proof, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "provider-execution.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
