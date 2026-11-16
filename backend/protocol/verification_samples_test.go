package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type verificationSampleSets struct {
	samples, tokenCounts []Sample
	models               []ModelSample
	tasks                []TaskSample
	agent                []AgentSample
	sessions             []SessionSample
}

func collectVerificationSampleSets(compiled *Compiled) verificationSampleSets {
	return verificationSampleSets{
		samples: compiled.verificationSamples(), tokenCounts: compiled.verificationTokenCountSamples(),
		models: compiled.verificationModelSamples(), tasks: compiled.verificationTaskSamples(),
		agent: compiled.verificationAgentSamples(), sessions: compiled.verificationSessionSamples(),
	}
}

func readVerificationPackage(t testing.TB, name string) *Compiled {
	t.Helper()
	read := func(part string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("builtin", "packages", name, part+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := append(fmt.Appendf(nil, `{"schemaVersion":%d,"spec":`, ProtocolPackageSchemaVersion), read("spec")...)
	raw = append(raw, []byte(`,"mapping":`)...)
	raw = append(raw, read("mapping")...)
	raw = append(raw, '}')
	value, err := ParseValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	var document ProtocolPackage
	if err := value.Decode(&document); err != nil {
		t.Fatal(err)
	}
	definition, err := EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	return &Compiled{definition: definition}
}

func TestVerificationSampleGettersMatchPackage(t *testing.T) {
	for _, name := range []string{"anthropic", "google-generate-content", "openai-chat", "openai-responses"} {
		t.Run(name, func(t *testing.T) {
			compiled := readVerificationPackage(t, name)
			document, _ := compiled.Package()
			for _, fixture := range []struct {
				name   string
				actual any
				want   any
			}{
				{"samples", compiled.verificationSamples(), document.Mapping.Samples},
				{"models", compiled.verificationModelSamples(), document.Mapping.ModelSamples},
				{"tasks", compiled.verificationTaskSamples(), document.Mapping.TaskSamples},
				{"agent", compiled.verificationAgentSamples(), document.Mapping.Agent.Samples},
				{"sessions", compiled.verificationSessionSamples(), document.Mapping.SessionSamples},
				{"token-count", compiled.verificationTokenCountSamples(), document.Mapping.TokenCountSamples},
			} {
				if !reflect.DeepEqual(fixture.actual, fixture.want) {
					t.Fatalf("%s differs from authored package", fixture.name)
				}
			}
		})
	}
}

func TestVerificationSampleGettersOwnNestedData(t *testing.T) {
	const exactPayload = `{"n":9007199254740993,"empty":null,"flag":false,"text":"a\\b"}`
	payload, err := ParseValue([]byte(exactPayload))
	if err != nil {
		t.Fatal(err)
	}
	sample := Sample{
		ID: "sample", Direction: DecodeRequest, Capabilities: []Capability{TextCapability},
		Input: payload, Expected: payload, ExpectedIssue: InvalidInput,
		Context: Object{"fixture": payload}, Scope: Scope{Provider: "provider", Model: "model"},
	}
	document := ProtocolPackage{Mapping: ProtocolMapping{
		Samples: []Sample{sample}, TokenCountSamples: []Sample{sample},
		ModelSamples: []ModelSample{{ID: "models", Input: payload, Expected: payload}},
		TaskSamples:  []TaskSample{{ID: "task", Input: payload, Expected: payload}},
		Agent:        &ProtocolAgentMapping{Samples: []AgentSample{{ID: "agent", Model: "model", Expected: payload, ExpectedClientOutput: payload}}},
		SessionSamples: []SessionSample{{ID: "session", Model: payload, Context: Object{"fixture": payload},
			Steps: []SessionStep{{Direction: DecodeEvent, Input: payload, Expected: payload}}}},
	}}
	definition, err := EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	compiled := &Compiled{definition: definition}
	want := collectVerificationSampleSets(compiled)
	wantPackage, _ := compiled.Package()
	if want.samples[0].Input.JSONString() != exactPayload || want.tokenCounts[0].Expected.JSONString() != exactPayload {
		t.Fatal("sample projection changed exact JSON values", want)
	}
	for range 4 {
		t.Run("isolated", func(t *testing.T) {
			t.Parallel()
			actual := collectVerificationSampleSets(compiled)
			actual.samples[0].Capabilities[0] = NativeExtensionsCapability
			actual.samples[0].Context["fixture"] = StringValue("changed")
			actual.samples[0].Scope.Model = "changed"
			actual.samples[0].ExpectedIssue = UnsupportedCapability
			actual.tokenCounts[0].Capabilities[0] = NativeExtensionsCapability
			actual.tokenCounts[0].Context["fixture"] = StringValue("changed")
			actual.models[0].Expected = StringValue("changed")
			actual.tasks[0].Input = StringValue("changed")
			actual.agent[0].ExpectedClientOutput = StringValue("changed")
			actual.agent[0].Preferences.ThinkingEnabled = true
			actual.sessions[0].Context["fixture"] = StringValue("changed")
			actual.sessions[0].Steps[0].Expected = StringValue("changed")
			if got := collectVerificationSampleSets(compiled); !reflect.DeepEqual(got, want) {
				t.Fatal("getter mutation reached later fixture reads")
			}
			copy, _ := compiled.Package()
			if !reflect.DeepEqual(copy, wantPackage) {
				t.Fatal("getter mutation reached the public authored package")
			}
			copy.Mapping.Samples[0].Context["fixture"] = StringValue("changed")
			copy.Mapping.Agent.Samples[0].Expected = StringValue("changed")
			if got := collectVerificationSampleSets(compiled); !reflect.DeepEqual(got, want) {
				t.Fatal("public package mutation reached fixture reads")
			}
		})
	}
}

func TestVerificationSampleGettersPreserveOptionalSets(t *testing.T) {
	for _, fixture := range []struct {
		name, mapping string
		isEmpty       bool
	}{
		{"absent", `{}`, false},
		{"null", `{"samples":null,"modelSamples":null,"taskSamples":null,"tokenCountSamples":null,"sessionSamples":null,"agent":{"samples":null}}`, false},
		{"empty", `{"samples":[],"modelSamples":[],"taskSamples":[],"tokenCountSamples":[],"sessionSamples":[],"agent":{"samples":[]}}`, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			definition, err := ParseValue([]byte(`{"mapping":` + fixture.mapping + `}`))
			if err != nil {
				t.Fatal(err)
			}
			sets := collectVerificationSampleSets(&Compiled{definition: definition})
			for _, samples := range []any{sets.samples, sets.models, sets.tasks, sets.tokenCounts, sets.agent, sets.sessions} {
				value := reflect.ValueOf(samples)
				if value.Len() != 0 || value.IsNil() == fixture.isEmpty {
					t.Fatal("optional sample presence changed", samples)
				}
			}
		})
	}
}

func BenchmarkVerificationSampleGetters(b *testing.B) {
	for _, name := range []string{"openai-chat", "openai-responses"} {
		compiled := readVerificationPackage(b, name)
		for _, getter := range []struct {
			name string
			read func() int
		}{
			{"full-package-samples", func() int { document, _ := compiled.Package(); return len(document.Mapping.Samples) }},
			{"selected-samples", func() int { return len(compiled.verificationSamples()) }},
			{"full-package-agent", func() int { document, _ := compiled.Package(); return len(document.Mapping.Agent.Samples) }},
			{"selected-agent", func() int { return len(compiled.verificationAgentSamples()) }},
		} {
			b.Run(name+"/"+getter.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if getter.read() == 0 {
						b.Fatal("missing authored samples")
					}
				}
			})
		}
	}
}
