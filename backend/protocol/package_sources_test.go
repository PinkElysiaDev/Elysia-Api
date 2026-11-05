package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompileSourcesSharesPackageIdentityAndEvidence(t *testing.T) {
	document := verifiedTextPackage(t, "source-pair")
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	whole, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	managed, managedIssues := compiler.Compile(fixtureValue(t, document).Bytes())
	if err := IssuesError(managedIssues); err != nil || managed.Hash() != whole.Hash() {
		t.Fatal("management compiler changed the package identity", err)
	}
	sources, issues := compiler.CompileSources(fixtureValue(t, document.Spec).Bytes(), fixtureValue(t, document.Mapping).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if sources.Hash() != whole.Hash() || sources.SamplesHash() != whole.SamplesHash() || sources.definition != whole.definition {
		t.Fatal("source pair changed the immutable package identity")
	}
	fromSources, hasSources := sources.Package()
	fromWhole, hasWhole := whole.Package()
	if !hasSources || !hasWhole || !reflect.DeepEqual(fromSources, fromWhole) {
		t.Fatal("source pair changed the stored package")
	}
	wholeReport, sourceReport := Verify(t.Context(), whole), Verify(t.Context(), sources)
	if !wholeReport.Passed || !sourceReport.Passed || !reflect.DeepEqual(wholeReport.Checks, sourceReport.Checks) || IssuesError(CanActivate(sources, wholeReport)) != nil {
		t.Fatal("source pair changed offline verification", wholeReport, sourceReport)
	}
	for _, source := range []string{"spec", "mapping"} {
		t.Run(source, func(t *testing.T) {
			changed := verifiedTextPackage(t, "source-pair")
			if source == "spec" {
				changed.Spec.Version = "2"
			} else {
				changed.Mapping.Samples[0].ID = "renamed-request"
			}
			compiled, issues := compiler.CompileSources(fixtureValue(t, changed.Spec).Bytes(), fixtureValue(t, changed.Mapping).Bytes())
			if err := IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			if compiled.Hash() == whole.Hash() || IssuesError(CanActivate(compiled, wholeReport)) == nil {
				t.Fatal("changed source reused the previous revision or evidence")
			}
		})
	}
}

func TestCompileSourcesRejectsInvalidDocuments(t *testing.T) {
	document := textProtocolPackage(t)
	spec, mapping := fixtureValue(t, document.Spec).JSONString(), fixtureValue(t, document.Mapping).JSONString()
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, spec, mapping, path string }{
		{"spec-unknown", strings.Replace(spec, "{", `{"unknown":true,`, 1), mapping, "/spec"},
		{"spec-trailing", spec + " {}", mapping, "/spec"},
		{"mapping-unknown", spec, strings.Replace(mapping, `"program":`, `"module":"responses","program":`, 1), "/mapping"},
		{"mapping-trailing", spec, mapping + " {}", "/mapping"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled, issues := compiler.CompileSources([]byte(fixture.spec), []byte(fixture.mapping))
			if compiled != nil || len(issues) != 1 || issues[0].Code != InvalidDefinition || !strings.HasPrefix(issues[0].Path, fixture.path) {
				t.Fatal("invalid source escaped strict parsing", issues)
			}
		})
	}
}

func TestCompileSourcesEnforcesWholePackageLimits(t *testing.T) {
	document := textProtocolPackage(t)
	spec, mapping := fixtureValue(t, document.Spec), fixtureValue(t, document.Mapping)
	whole := fixtureValue(t, document)
	limits := DefaultLimits()
	specMeter, mappingMeter := valueTokenMeter{limits: limits}, valueTokenMeter{limits: limits}
	if err := specMeter.scan(spec.raw); err != nil {
		t.Fatal(err)
	}
	if err := mappingMeter.scan(mapping.raw); err != nil {
		t.Fatal(err)
	}
	for _, dimension := range []string{"bytes", "nodes", "depth"} {
		t.Run(dimension, func(t *testing.T) {
			limited := limits
			switch dimension {
			case "bytes":
				limited.BufferBytes = spec.Len() + mapping.Len()
			case "nodes":
				limited.Nodes = max(specMeter.nodes, mappingMeter.nodes)
			case "depth":
				for limited.Depth = 1; limited.Depth < limits.Depth; limited.Depth++ {
					if checkValueLimits(spec, limited) == nil && checkValueLimits(mapping, limited) == nil {
						break
					}
				}
			}
			if err := checkValueLimits(spec, limited); err != nil {
				t.Fatal("fixture spec must fit by itself", err)
			}
			if err := checkValueLimits(mapping, limited); err != nil {
				t.Fatal("fixture mapping must fit by itself", err)
			}
			compiler, err := NewCompiler(limited, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			for name, compile := range map[string]func() (*Compiled, []ConversionIssue){
				"sources": func() (*Compiled, []ConversionIssue) { return compiler.CompileSources(spec.Bytes(), mapping.Bytes()) },
				"package": func() (*Compiled, []ConversionIssue) { return compiler.CompilePackage(whole.Bytes()) },
			} {
				compiled, issues := compile()
				if compiled != nil || len(issues) != 1 || issues[0].Code != InvalidDefinition || !strings.Contains(issues[0].Reason, dimension) && !(dimension == "bytes" && strings.Contains(issues[0].Reason, "buffer")) {
					t.Fatal(name, "bypassed the combined package budget", issues)
				}
			}
		})
	}
	t.Run("individual-bytes", func(t *testing.T) {
		limited := limits
		limited.BufferBytes = whole.Len()
		compiler, err := NewCompiler(limited, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		padded := strings.Repeat(" ", limited.BufferBytes-mapping.Len()+1) + mapping.raw
		compiled, issues := compiler.CompileSources(spec.Bytes(), []byte(padded))
		if compiled != nil || len(issues) != 1 || issues[0].Path != "/mapping" || !strings.Contains(issues[0].Reason, "source exceeds buffer") {
			t.Fatal("source whitespace bypassed its byte limit", issues)
		}
	})
}
