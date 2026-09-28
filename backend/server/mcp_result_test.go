package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMCPToolResultTextAndStructuredContent(t *testing.T) {
	for _, era := range []mcpEra{mcpEraLegacy, mcpEraModern} {
		for _, failed := range []bool{false, true} {
			data := map[string]any{"output": "source-a\nsource-b", "exitCode": 0}
			var callErr error
			if failed {
				data["exitCode"] = 1
				callErr = errors.New("second command failed")
			}
			response := mcpToolCallResult(json.RawMessage(`1`), era, data, callErr)
			payload := response.Result.(gin.H)
			if payload["isError"] != failed {
				t.Fatalf("wrong failure flag: %v", payload)
			}
			content := payload["content"].([]gin.H)
			var textData map[string]any
			if err := json.Unmarshal([]byte(content[0]["text"].(string)), &textData); err != nil {
				t.Fatal(err)
			}
			if textData["output"] != data["output"] || payload["structuredContent"] == nil {
				t.Fatalf("result lost from text or structured content: %v", payload)
			}
			if failed && (len(content) != 2 || !strings.Contains(content[1]["text"].(string), callErr.Error())) {
				t.Fatalf("failure details lost: %v", content)
			}
		}
	}
}
