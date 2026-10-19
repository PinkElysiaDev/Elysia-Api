package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestCLISecretRenderingBeforeLossyOperations(t *testing.T) {
	secret := "review-secret-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for _, position := range []int{cliOutputHeadBytes - 14, 36000 - cliOutputTailBytes - 14} {
		output := cliStatementOutput{text: strings.Repeat("x", position) + secret + strings.Repeat("y", 36000-position)}
		masked := clampCLIOutput(output.render([]string{secret}))
		if strings.Contains(masked, "review-secret") || strings.Contains(masked, "GHIJKLMNOPQRSTUVWXYZ") {
			t.Fatal("secret fragment survived truncation")
		}
		if !strings.Contains(output.render(nil), secret) {
			t.Fatal("live copy lost secret")
		}
	}
	output := cliStatementOutput{text: "prefix\nfirst\nsecond\nsuffix", statement: cliStatement{pipes: []cliPipe{{kind: pipeHead, n: 2}}}}
	masked := output.render([]string{"first\nsecond"})
	if strings.Contains(masked, "first") {
		t.Fatal("pipe split secret before masking")
	}
	result := agentResultFromCLI(CLIResult{Data: map[string]string{"output": "live"}, RedactedData: []byte(`{"output":"***"}`)})
	if string(result.RedactedData) != `{"output":"***"}` {
		t.Fatal("adapter dropped prepared model data")
	}
}

func TestCLISecretIsMaskedBeforeJSONRendering(t *testing.T) {
	server := newOpsTestServer(t)
	secret := `sensitive-before"<value>&\tail`
	result := server.runCLI(t.Context(), &opsTestContext{}, fmt.Sprintf("elysia key create --name escaped --secret '%s'", secret))
	if !result.OK {
		t.Fatalf("create failed: %s", result.Summary)
	}
	var stored struct {
		Output string `json:"output"`
	}
	if err := sonic.ConfigStd.Unmarshal(result.RedactedData, &stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Output, "sensitive-before") || strings.Contains(stored.Output, `\u003cvalue`) {
		t.Fatal("JSON escaping concealed secret from redaction")
	}
	live := result.Data.(map[string]any)["output"].(string)
	if !strings.Contains(live, secret) {
		t.Fatal("one-time client output lost the original value")
	}
}
