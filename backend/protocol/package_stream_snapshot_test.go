package protocol

import "testing"

func toolSnapshotStream(t *testing.T) *ProtocolStreamSpec {
	t.Helper()
	spec := toolStreamPackage(t).Spec.Documents["provider-event"].Stream
	spec.OptionalSequence = true
	spec.Events["tool.snapshot"] = ProtocolStreamEventSpec{{Action: "tool_snapshot", Arguments: "/arguments"}}
	return spec
}

func TestPackageStreamToolSnapshotsAndOptionalSequence(t *testing.T) {
	state := newSparseIdentityValidation(t, toolSnapshotStream(t))
	if err := state.consume(t.Context(), Value{raw: `{"kind":"response.started","response_id":"r"}`}); err != nil || state.accepted.hasSequence {
		t.Fatal("missing initial sequence invented a watermark", err)
	}
	for _, raw := range []string{
		`{"kind":"tool.started","response_id":"r","item_id":"call","seq":4,"name":"lookup","call_id":"call"}`,
		`{"kind":"tool.delta","response_id":"r","item_id":"call","arguments":"{\"n\":9"}`,
		`{"kind":"tool.snapshot","response_id":"r","item_id":"call","seq":5,"arguments":"{\"n\":9007199254740993}"}`,
		`{"kind":"tool.snapshot","response_id":"r","item_id":"call","seq":6,"arguments":"{ \"n\" : 9007199254740993 }"}`,
		`{"kind":"tool.finished","response_id":"r","item_id":"call"}`,
	} {
		if err := state.consume(t.Context(), Value{raw: raw}); err != nil {
			t.Fatal(raw, err)
		}
	}
	if !state.accepted.hasSequence || state.accepted.sequence != 6 || state.replay.state.buffered != 0 {
		t.Fatal("missing sequence reset its watermark or completed input remained buffered")
	}
	if err := state.consume(t.Context(), Value{raw: `{"kind":"response.finished","response_id":"r","seq":7}`}); err != nil || state.finish() != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ raw, path string }{
		{`{"kind":"tool.snapshot","response_id":"r","item_id":"call","arguments":"{\"n\":2}"}`, "/arguments"},
		{`{"kind":"tool.snapshot","response_id":"r","item_id":"call","arguments":null}`, "/arguments"},
		{`{"kind":"tool.snapshot","response_id":"r","item_id":"call","seq":null,"arguments":"{\"n\":1}"}`, "/seq"},
		{`{"kind":"tool.snapshot","response_id":"r","item_id":"call","seq":4,"arguments":"{\"n\":1}"}`, "/seq"},
	} {
		state := newSparseIdentityValidation(t, toolSnapshotStream(t))
		for _, frame := range []Value{packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(packageToolStarted, 4, `,"item_id":"call","name":"lookup","call_id":"call"`), {raw: `{"kind":"tool.delta","response_id":"r","item_id":"call","arguments":"{\"n\":1}"}`}} {
			if err := state.consume(t.Context(), frame); err != nil {
				t.Fatal(err)
			}
		}
		assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: fixture.raw}), UpstreamContractViolation, fixture.path)
	}
}

func TestPackageStreamSnapshotCompilation(t *testing.T) {
	for _, edit := range []func(*ProtocolStreamSpec){
		func(spec *ProtocolStreamSpec) { spec.Sequence = "" },
		func(spec *ProtocolStreamSpec) { spec.Events["tool.snapshot"][0].Arguments = "" },
		func(spec *ProtocolStreamSpec) {
			spec.Events["tool.snapshot"][0].Tool = &ProtocolStreamToolSpec{Name: "/name", CallID: "/call_id"}
		},
	} {
		spec := toolSnapshotStream(t)
		edit(spec)
		if _, err := compilePackageStreamContract(spec, "/stream", DefaultLimits()); err == nil {
			t.Fatal("invalid optional sequence or tool snapshot declaration compiled")
		}
	}
}

func TestPackageStreamKeyedRawIdentities(t *testing.T) {
	spec := sparseIdentityStream(t)
	spec.Events["heartbeat"] = ProtocolStreamEventSpec{{Action: "control"}}
	spec.Identities = []ProtocolStreamIdentitySpec{{Path: "/wire_id", Type: StringType,
		Key: &Expression{Op: "cast", Source: expressionPointer(readExpression("input", "/index")), To: StringType}, KeyPath: "/index", Comparison: "raw_json", Unique: true,
		Diagnostic: &RejectDiagnostic{Code: UnsupportedCapability, Stage: "wire"},
	}}
	for _, fixture := range []struct{ raw, path string }{
		{`{"type":"heartbeat","index":0,"wire_id":"\u0061"}`, "/wire_id"},
		{`{"type":"heartbeat","index":1,"wire_id":"a"}`, "/index"},
		{`{"type":"heartbeat","index":0,"wire_id":null}`, "/wire_id"},
	} {
		state := newSparseIdentityValidation(t, spec)
		if err := state.consume(t.Context(), Value{raw: `{"type":"heartbeat","index":false}`}); err != nil || state.identityItems != 0 {
			t.Fatal("missing identity evaluated or retained its key", err)
		}
		if err := state.consume(t.Context(), Value{raw: `{"type":"heartbeat","index":0,"wire_id":"a"}`}); err != nil {
			t.Fatal(err)
		}
		assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: fixture.raw}), UnsupportedCapability, fixture.path)
	}
	state := newSparseIdentityValidation(t, spec)
	for _, raw := range []string{`{"type":"heartbeat","index":0,"wire_id":"a"}`, `{"type":"heartbeat","index":1,"wire_id":"\u0061"}`} {
		if err := state.consume(t.Context(), Value{raw: raw}); err != nil {
			t.Fatal("raw identity equality changed into decoded string equality", err)
		}
	}
	spec.Identities[0].Comparison = ""
	state = newSparseIdentityValidation(t, spec)
	for _, raw := range []string{`{"type":"heartbeat","index":0,"wire_id":"a"}`, `{"type":"heartbeat","index":0,"wire_id":"\u0061"}`} {
		if err := state.consume(t.Context(), Value{raw: raw}); err != nil {
			t.Fatal("default typed equality changed", err)
		}
	}
}
