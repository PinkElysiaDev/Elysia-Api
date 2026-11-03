package protocol

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ProtocolObservedField identifies a field in one declared direction.
type ProtocolObservedField struct {
	Direction Direction `json:"direction"`
	Path      string    `json:"path"`
}

// ProtocolFieldObservation relates dispatch, provider and core values without
// retaining their enclosing documents or inferring a protocol family.
type ProtocolFieldObservation struct {
	Transport Transport             `json:"transport"`
	Request   ProtocolObservedField `json:"request"`
	Response  ProtocolObservedField `json:"response"`
	Core      ProtocolObservedField `json:"core"`
	Output    ProtocolObservedField `json:"output"`
}

type compiledFieldObservation struct {
	declaration                     ProtocolFieldObservation
	request, response, core, output []string
}

type observedField struct {
	Present bool  `json:"present"`
	Value   Value `json:"value,omitzero"`
}

type fieldObservation struct {
	Request    observedField `json:"request"`
	Response   observedField `json:"response"`
	source     Identity
	target     Identity
	definition compiledFieldObservation
	output     []string
	outputPath string
}

func (compiled *Compiled) compileFieldObservations(declarations map[string]ProtocolFieldObservation) error {
	if len(declarations) > compiled.limits.StateItems {
		return fmt.Errorf("/mapping/fieldObservations: exceeds state item limit")
	}
	if len(declarations) == 0 {
		return nil
	}
	compiled.packagePlan.fieldObservations = make(map[string]compiledFieldObservation, len(declarations))
	for name, declaration := range declarations {
		path := "/mapping/fieldObservations/" + escapePointer(name)
		if !definitionIdentifier.MatchString(name) || declaration.Transport != HTTPJSON || declaration.Request.Direction != EncodeRequest || declaration.Response.Direction != DecodeResponse || declaration.Core.Direction != DecodeResponse || declaration.Output.Direction != EncodeResponse {
			return fmt.Errorf("%s: requires declared HTTP request/response directions", path)
		}
		plan := compiledFieldObservation{declaration: declaration}
		for _, field := range []struct {
			definition ProtocolObservedField
			pointer    *[]string
		}{
			{declaration.Request, &plan.request}, {declaration.Response, &plan.response}, {declaration.Core, &plan.core}, {declaration.Output, &plan.output},
		} {
			if _, exists := compiled.mappings[field.definition.Direction]; !exists {
				return fmt.Errorf("%s: observed direction is not implemented", path)
			}
			pointer, err := parsePointer(field.definition.Path)
			if err != nil || len(pointer) == 0 {
				return fmt.Errorf("%s: observation requires a non-root field pointer", path)
			}
			*field.pointer = pointer
		}
		if !isObservedCoreField(plan.core) {
			return fmt.Errorf("%s/core: field is not declared by the core response", path)
		}
		compiled.packagePlan.fieldObservations[name] = plan
	}
	return nil
}

func isObservedCoreField(pointer []string) bool {
	if len(pointer) != 1 {
		return false
	}
	root := reflect.TypeFor[Response]()
	for index := range root.NumField() {
		field := root.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == pointer[0] {
			return field.Type == reflect.TypeFor[Value]()
		}
	}
	return false
}

func (route ConversionContext) observeRequestFields(ingress, upstream *Compiled, outbound Value) (ConversionContext, error) {
	if route.Transport != HTTPJSON || ingress == nil || upstream == nil || ingress.packagePlan == nil || upstream.packagePlan == nil {
		return route, nil
	}
	reader := newNativeComparisonReader(outbound)
	for name, source := range upstream.packagePlan.fieldObservations {
		target, exists := ingress.packagePlan.fieldObservations[name]
		if !exists || source.declaration.Core != target.declaration.Core || source.declaration.Transport != route.Transport {
			continue
		}
		value, err := readValuePointerWithin(outbound, source.request, reader)
		if err != nil {
			return route, err
		}
		if route.fieldObservations == nil {
			route.fieldObservations = make(map[string]fieldObservation)
		}
		route.fieldObservations[name] = fieldObservation{Request: observeField(value), source: upstream.identity, target: ingress.identity,
			definition: source, output: target.output, outputPath: target.declaration.Output.Path}
	}
	return route, nil
}

func hasFieldObservationPair(ingress, upstream *Compiled) bool {
	if ingress == nil || upstream == nil || ingress.packagePlan == nil || upstream.packagePlan == nil {
		return false
	}
	for name, source := range upstream.packagePlan.fieldObservations {
		if target, exists := ingress.packagePlan.fieldObservations[name]; exists && source.declaration.Core == target.declaration.Core {
			return true
		}
	}
	return false
}

func observeField(value Value) observedField {
	return observedField{Present: !value.IsZero(), Value: Value{raw: strings.Clone(value.raw)}}
}

func (route ConversionContext) observeResponseFields(body []byte) (ConversionContext, error) {
	if len(route.fieldObservations) == 0 {
		return route, nil
	}
	value, err := ParseValue(body)
	if err != nil {
		return route, err
	}
	reader := newNativeComparisonReader(value)
	observations := make(map[string]fieldObservation, len(route.fieldObservations))
	for name, observation := range route.fieldObservations {
		selected, err := readValuePointerWithin(value, observation.definition.response, reader)
		if err != nil {
			return route, err
		}
		observation.Response = observeField(selected)
		observations[name] = observation
	}
	route.fieldObservations = observations
	return route, nil
}

func (route ConversionContext) attributeObservedFieldFailure(err error, response *Response, wire Value) error {
	var failure *ConversionError
	if response == nil || len(route.fieldObservations) == 0 || !errors.As(err, &failure) {
		return err
	}
	var issues []ConversionIssue
	for _, observation := range route.fieldObservations {
		if observation.source != route.Source || observation.target != route.Target {
			continue
		}
		core, readErr := readCoreResponseOutput(response, observation.definition.core, nil)
		if readErr != nil || core.node != nil || core.value != observation.Response.Value {
			continue
		}
		value, readErr := readValuePointer(wire, observation.output)
		if readErr != nil || !value.IsZero() != observation.Response.Present || value != observation.Response.Value {
			continue
		}
		for index, issue := range failure.Issues {
			if issue.Path != observation.outputPath || issue.Direction != EncodeResponse || issue.Code != ConversionRejected {
				continue
			}
			if issues == nil {
				issues = append([]ConversionIssue(nil), failure.Issues...)
			}
			issues[index].Code, issues[index].Protocol, issues[index].Direction = UpstreamContractViolation, route.Source, DecodeResponse
			issues[index].Stage, issues[index].Path = "decode.response", observation.definition.declaration.Response.Path
		}
	}
	if issues == nil {
		return err
	}
	return &ConversionError{cause: err, Issues: issues}
}
