package server

import (
	"strings"
	"testing"
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
