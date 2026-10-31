package protocol

import (
	"strconv"
	"strings"
)

type streamIdentityBindings struct {
	values map[string]string
	keys   map[string]string
}

func (state *packageStreamValidation) bindIdentities(input evaluation) error {
	read := state.fieldReader(input)
	for index, identity := range state.contract.identities {
		isApplicable, err := state.matches(identity.when, input, identity.field.path)
		if err != nil {
			return err
		}
		if !isApplicable {
			continue
		}
		value, err := read(identity.field)
		if err != nil {
			return err
		}
		// Missing values do not claim a key. Explicit null still fails the
		// declared identity type instead of replacing an established value.
		if value.IsZero() {
			continue
		}
		normalized, err := state.normalizeIdentity(identity, value)
		if err != nil {
			return err
		}
		if identity.key != nil {
			if err := state.bindKeyedIdentity(index, identity, normalized, input); err != nil {
				return err
			}
			continue
		}
		if previous := state.identities[index]; previous != "" {
			if previous != normalized {
				return state.boundaryIssue(identity.diagnostic, identity.field.path, "stream identity changed after it was established", nil)
			}
			continue
		}
		if err := state.checkRetainedBytes(state.identityBytes+len(normalized), identity.field.path); err != nil {
			return err
		}
		state.identities[index] = strings.Clone(normalized)
		state.identityBytes += len(normalized)
	}
	return nil
}

func (state *packageStreamValidation) normalizeIdentity(identity compiledStreamIdentity, value Value) (string, error) {
	var normalized string
	if identity.kind == IntegerType {
		count, err := state.readCount(value, identity.field.path)
		if err != nil {
			if identity.diagnostic == nil {
				return "", err
			}
			return "", state.boundaryIssue(identity.diagnostic, identity.field.path, "stream identity must be a nonnegative integer", err)
		}
		normalized = strconv.FormatInt(count, 10)
	} else {
		var err error
		normalized, err = state.readIdentityValue(value, identity.field.path)
		if err != nil {
			if identity.diagnostic == nil {
				return "", err
			}
			return "", state.boundaryIssue(identity.diagnostic, identity.field.path, "stream identity must be a nonempty string", err)
		}
	}
	if identity.isRaw {
		normalized = value.raw
	}
	return normalized, nil
}

func (state *packageStreamValidation) bindKeyedIdentity(index int, identity compiledStreamIdentity, value string, input evaluation) error {
	keyValue, err := identity.key.evaluate(input)
	if err != nil {
		return state.boundaryIssue(identity.diagnostic, identity.keyPath, "stream identity key expression failed", err)
	}
	key, err := state.readIdentityValue(keyValue, identity.keyPath)
	if err != nil {
		return state.boundaryIssue(identity.diagnostic, identity.keyPath, "stream identity key must be a nonempty string", err)
	}
	var bindings streamIdentityBindings
	if state.identityBindings != nil {
		bindings = state.identityBindings[index]
	}
	if previous := bindings.values[key]; previous != "" {
		if previous != value {
			return state.boundaryIssue(identity.diagnostic, identity.field.path, "stream identity changed at its established key", nil)
		}
		return nil
	}
	if identity.isUnique && bindings.keys[value] != "" {
		return state.boundaryIssue(identity.diagnostic, identity.keyPath, "stream identity was reused at another key", nil)
	}
	if state.identityItems >= state.limits.StateItems {
		return state.boundaryIssue(identity.diagnostic, identity.field.path, "stream identity bindings exceed the item limit", resourceFailure("stream", "identity_items", state.limits.StateItems, state.identityItems+1))
	}
	bytes := len(key) + len(value)
	if err := state.checkRetainedBytes(state.identityBytes+bytes, identity.field.path); err != nil {
		return state.boundaryIssue(identity.diagnostic, identity.field.path, "stream identity bindings exceed the buffer limit", err)
	}
	if state.identityBindings == nil {
		state.identityBindings = make([]streamIdentityBindings, len(state.contract.identities))
	}
	if bindings.values == nil {
		bindings.values = make(map[string]string)
		if identity.isUnique {
			bindings.keys = make(map[string]string)
		}
	}
	key, value = strings.Clone(key), strings.Clone(value)
	bindings.values[key] = value
	if identity.isUnique {
		bindings.keys[value] = key
	}
	state.identityBindings[index] = bindings
	state.identityItems++
	state.identityBytes += bytes
	return nil
}

func (state *packageStreamValidation) boundaryIssue(diagnostic *RejectDiagnostic, path, reason string, cause error) error {
	if diagnostic == nil {
		return state.issue(path, reason, cause)
	}
	direction := state.direction
	if diagnostic.Direction != "" {
		direction = diagnostic.Direction
	}
	if diagnostic.Path != "" {
		path = diagnostic.Path
	}
	return &ConversionError{cause: cause, Issues: []ConversionIssue{{Code: diagnostic.Code, Severity: SeverityError,
		Protocol: state.target.Protocol, Direction: direction, Stage: diagnostic.Stage, Path: path, Reason: reason, Suggestion: diagnostic.Suggestion}}}
}
