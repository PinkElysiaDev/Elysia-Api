package protocol

import "testing"

func TestDeclaredUsageAliasesPreserveZeroMissingAndPriority(t *testing.T) {
	for _, path := range []string{"/usage/prompt_cache_hit_tokens", "/timings/cache_n", "/choices/0/usage/prompt_cache_hit_tokens"} {
		t.Run(path, func(t *testing.T) {
			definition := textVerificationDefinition(t, "declared-usage")
			definition.Capabilities[UsageCapability] = true
			primary := readExpression("input", "/meter/cache")
			alias := readExpression("input", path)
			count := Expression{Op: "if", When: &Expression{Op: "exists", Source: &primary}, Then: &primary, Otherwise: &alias}
			counter := Expression{Op: "if", When: &Expression{Op: "exists", Source: &count}, Then: expressionPointer(objectExpression(map[string]Expression{
				"count": count, "origin": literalExpression("observed"),
			}))}
			mapping := definition.Directions[DecodeResponse]
			mapping.Transform.Fields["usage"] = objectExpression(map[string]Expression{"cacheRead": counter})
			definition.Directions[DecodeResponse] = mapping
			compiled := compileTestDefinition(t, definition)
			for _, fixture := range []struct {
				input string
				want  int64
			}{
				{`{"answer":[]}`, -1},
				{`{"answer":[],"usage":{"prompt_cache_hit_tokens":70},"timings":{"cache_n":70},"choices":[{"usage":{"prompt_cache_hit_tokens":70}}]}`, 70},
				{`{"answer":[],"meter":{"cache":0},"usage":{"prompt_cache_hit_tokens":70},"timings":{"cache_n":70},"choices":[{"usage":{"prompt_cache_hit_tokens":70}}]}`, 0},
			} {
				response, err := compiled.DecodeResponse(t.Context(), []byte(fixture.input), EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				counter := response.Usage.CacheRead
				if fixture.want < 0 {
					if counter != nil {
						t.Fatal("missing alias became an observed zero")
					}
				} else if counter == nil || counter.Count != fixture.want || counter.Origin != ObservedCount {
					t.Fatal("declared precedence lost", counter)
				}
			}
		})
	}
}
