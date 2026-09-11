package relay

import (
	"strings"
	"testing"
)

func TestNormalizeCustomProtocolType(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", CustomProtocolTypeLLM},
		{"   ", CustomProtocolTypeLLM},
		{"llm", CustomProtocolTypeLLM},
		{"LLM", CustomProtocolTypeLLM},
		{" Reranker ", CustomProtocolTypeReranker},
		{"embedding", CustomProtocolTypeEmbedding},
		{"x-vision", "x-vision"},
		{"X-Audio", "x-audio"},
		{"bogus", ""},
		{"x-", ""},
	}
	for _, tc := range cases {
		if got := NormalizeCustomProtocolType(tc.input); got != tc.want {
			t.Errorf("NormalizeCustomProtocolType(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestValidateCustomProtocolType(t *testing.T) {
	base := func() CustomProtocolConfig {
		return CustomProtocolConfig{
			ID:      "t1",
			Request: CustomProtocolRequest{BodyTemplate: `{"q":{{maheshvara.model | json}}}`},
		}
	}
	if err := ValidateCustomProtocol(base()); err != nil {
		t.Fatalf("empty type should default to llm: %v", err)
	}
	config := base()
	config.Type = "reranker"
	if err := ValidateCustomProtocol(config); err != nil {
		t.Fatalf("reranker should validate: %v", err)
	}
	config.Type = "x-tts"
	if err := ValidateCustomProtocol(config); err != nil {
		t.Fatalf("x-* extension should validate: %v", err)
	}
	config.Type = "invalid"
	err := ValidateCustomProtocol(config)
	if err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("invalid type should be rejected, got %v", err)
	}
}

func TestReplaceCustomProtocolsNormalizesType(t *testing.T) {
	ClearCustomProtocols()
	t.Cleanup(ClearCustomProtocols)
	config := CustomProtocolConfig{
		ID:      "norm-type",
		Request: CustomProtocolRequest{BodyTemplate: `{"q":{{maheshvara.model | json}}}`},
	}
	if err := RegisterCustomProtocol(config); err != nil {
		t.Fatalf("register: %v", err)
	}
	stored, ok := GetCustomProtocol("norm-type")
	if !ok || stored.Type != CustomProtocolTypeLLM {
		t.Fatalf("registry should store normalized type, got %#v", stored.Type)
	}
}
