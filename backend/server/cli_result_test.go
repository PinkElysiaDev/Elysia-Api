package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCLIRejectsInvalidResultData(t *testing.T) {
	for _, data := range []any{json.RawMessage(`not-json`), make(chan int)} {
		result := normalizeCLIResult(CLIResult{OK: true, Data: data})
		if result.OK || !strings.Contains(renderCLIResult(result, cliStatement{}), "invalid_tool_result") {
			t.Fatalf("invalid result reported success: %+v", result)
		}
	}
	result := normalizeCLIResult(CLIResult{OK: true, Data: json.RawMessage(`{"n":9007199254740993}`)})
	if !result.OK || !strings.Contains(renderCLIResult(result, cliStatement{}), "9007199254740993") {
		t.Fatalf("numeric result changed: %+v", result)
	}
}
