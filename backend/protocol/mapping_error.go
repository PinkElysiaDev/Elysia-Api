package protocol

import (
	"errors"
	"fmt"
	"slices"
)

func validateDiagnosticClassification(code IssueCode, stage, path string) error {
	if !slices.Contains([]IssueCode{InvalidInput, InvalidAssociation, UnsupportedCapability, ConversionRejected, UpstreamContractViolation}, code) {
		return fmt.Errorf("%s/code: unsupported diagnostic classification", path)
	}
	if stage != "wire" && stage != "evaluate" && stage != "runtime" {
		return fmt.Errorf("%s/stage: requires wire, evaluate or runtime", path)
	}
	return nil
}

func validateBoundaryDiagnostic(diagnostic RejectDiagnostic, path string) error {
	stage := diagnostic.Stage
	if stage == "" || stage == "stream" || stage == "decode" || stage == "decode.error" || stage == "wire.output" || stage == "request.validation" {
		stage = "wire"
	}
	if err := validateDiagnosticClassification(diagnostic.Code, stage, path); err != nil {
		return err
	}
	if diagnostic.Direction != "" && !slices.Contains(DirectionCatalog(), diagnostic.Direction) {
		return fmt.Errorf("%s/direction: unsupported direction", path)
	}
	if _, err := parsePointer(diagnostic.Path); err != nil {
		return locateMappingError(path+"/path", err)
	}
	return nil
}

// mappingError retains the innermost configuration location through nested
// expressions. Human-readable messages never need to be parsed by the editor.
type mappingError struct {
	path string
	err  error
}

func (failure *mappingError) Error() string { return failure.err.Error() }
func (failure *mappingError) Unwrap() error { return failure.err }

func locateMappingError(path string, err error) error {
	if err == nil {
		return nil
	}
	var located *mappingError
	if errors.As(err, &located) {
		return err
	}
	return &mappingError{path: path, err: err}
}

func mappingErrorPath(path string, err error) string {
	var located *mappingError
	if errors.As(err, &located) {
		return located.path
	}
	return path
}
