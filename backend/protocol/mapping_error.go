package protocol

import "errors"

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
