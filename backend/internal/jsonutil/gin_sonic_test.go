//go:build sonic && (linux || windows || darwin) && (amd64 || arm64)

package jsonutil

import (
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
	ginjson "github.com/gin-gonic/gin/codec/json"
)

func TestGinUsesSonic(t *testing.T) {
	if ginjson.Package != "github.com/bytedance/sonic" {
		t.Fatalf("Gin selected %s", ginjson.Package)
	}
	if sonic.APIKind != sonic.UseSonicJSON {
		t.Fatal("Sonic is using its standard-library fallback")
	}
	input := map[string]any{"z": "<>&", "a": json.RawMessage(" { \"n\": 1e+02 } ")}
	want := `{"a":{"n":1e+02},"z":"\u003c\u003e\u0026"}`
	got, err := ginjson.API.Marshal(input)
	if err != nil || string(got) != want {
		t.Fatalf("Gin JSON = %s, %v; want %s", got, err, want)
	}
}
