package protocol

import (
	"context"
	"errors"
	"testing"
)

type contractTestModule struct {
	failure                 error
	valueCalls, objectCalls int
}

func (*contractTestModule) Name() string { return "contract-fixture" }
func (*contractTestModule) Directions() []Direction {
	return []Direction{EncodeResponse, EncodeEvent}
}
func (*contractTestModule) Convert(_ context.Context, _ Direction, value Value, _ EvaluationContext) (Value, error) {
	return value, nil
}
func (module *contractTestModule) ValidateWireOutput(Direction, Value) error {
	module.valueCalls++
	return module.failure
}
func (module *contractTestModule) ValidateWireObjectOutput(Direction, Value, Object) error {
	module.objectCalls++
	return module.failure
}
func (*contractTestModule) NewStream(Direction, Limits) (Module, error) {
	return &contractTestStream{}, nil
}

type contractTestStream struct {
	contractTestModule
	hasStarted bool
}

var errContractRepeatedStart = errors.New("wire response started twice")

func (stream *contractTestStream) ValidateWireEvent(value Value) error {
	var event Event
	if err := value.Decode(&event); err != nil {
		return err
	}
	if event.Type == ResponseStarted {
		if stream.hasStarted {
			return errContractRepeatedStart
		}
		stream.hasStarted = true
	}
	return nil
}

func (*contractTestStream) Convert(_ context.Context, _ Direction, value Value, _ EvaluationContext) (Value, error) {
	var event Event
	if err := value.Decode(&event); err != nil {
		return Value{}, err
	}
	return EncodeValue([]Event{event})
}

func compileContractTestModule(t *testing.T, module *contractTestModule, direction Direction) *Compiled {
	t.Helper()
	definition := testDefinition("contract-binding", "model", "history", "tools", "reply")
	definition.Directions[direction] = Mapping{Module: module.Name()}
	compiler, err := NewCompiler(DefaultLimits(), []Module{module}, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := fixtureValue(t, definition)
	compiled, issues := compiler.Compile(value.Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestWireContractSurvivesMappingReplacement(t *testing.T) {
	failure := errors.New("invalid final wire")
	module := &contractTestModule{failure: failure}
	compiled := compileContractTestModule(t, module, EncodeResponse)
	mapping := compiled.mappings[EncodeResponse]
	mapping.module = nil
	compiled.mappings[EncodeResponse] = mapping
	value := fixtureValue(t, Object{"invalid": StringValue("output")})
	if err := compiled.ValidateWireOutput(EncodeResponse, value); !errors.Is(err, failure) {
		t.Fatal("replacing execution removed the wire contract", err)
	}
	fields, _ := value.ReadObject()
	if err := compiled.validateWireOutput(EncodeResponse, value, fields); !errors.Is(err, failure) {
		t.Fatal("object validation lost the pinned contract", err)
	}
	if module.valueCalls != 1 || module.objectCalls != 1 {
		t.Fatal("final output was checked more than once", module.valueCalls, module.objectCalls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := compiled.ValidateWireOutputContext(ctx, EncodeResponse, value); !errors.Is(err, failure) {
		t.Fatal("the context entrypoint changed the installed module contract", err)
	}
}

func TestWireContractStreamStateIsRequestOwned(t *testing.T) {
	module := &contractTestModule{}
	compiled := compileContractTestModule(t, module, EncodeEvent)
	contract := compiled.wireContracts[EncodeEvent]
	if contract.streamFactory != nil {
		t.Fatal("a custom module inherited a built-in stream contract")
	}
	// A wire contract explicitly supplies its lifecycle; the execution module's
	// name and presence must not authorize or disable that lifecycle at runtime.
	contract.streamFactory = module
	compiled.wireContracts[EncodeEvent] = contract
	mapping := compiled.mappings[EncodeEvent]
	mapping.module = nil
	compiled.mappings[EncodeEvent] = mapping
	first, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	start := fixtureValue(t, Event{SchemaVersion: 1, Type: ResponseStarted})
	finish := fixtureValue(t, Event{SchemaVersion: 1, Type: ResponseFinished})
	if err := first.Consume(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	if err := first.Consume(t.Context(), start); !errors.Is(err, errContractRepeatedStart) {
		t.Fatal("wire lifecycle validation was bypassed", err)
	}
	if err := second.Consume(t.Context(), start); err != nil {
		t.Fatal("wire lifecycle state leaked between requests", err)
	}
	if err := first.Consume(t.Context(), finish); err != nil {
		t.Fatal(err)
	}
	if err := first.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := second.Finish(); err == nil {
		t.Fatal("finishing one request completed the other request")
	}
	if err := second.Consume(t.Context(), finish); err != nil {
		t.Fatal(err)
	}
	if err := second.Finish(); err != nil {
		t.Fatal(err)
	}
}
