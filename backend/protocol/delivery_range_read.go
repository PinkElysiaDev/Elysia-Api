package protocol

import (
	"context"
	"fmt"
)

type deliveryTextObservation struct{ key, text, mode, path string }
type deliveryRangeObservation struct {
	key, id, path string
	start, end    int64
}

func deliveryEvaluation(ctx context.Context, view *inputView, limits Limits) evaluation {
	return evaluation{ctx: ctx, input: view.value, root: view.value, inputView: view,
		valueReader: &view.borrowed, objectReader: view.objectReader(), budget: &evaluationBudget{limits: limits, stepLimit: limits.Nodes}}
}

func readDeliveryRecords(plan *compiledOutputPlan, state evaluation) ([]JSONOutput, error) {
	value, err := plan.build(state)
	if err != nil {
		return nil, err
	}
	return plannedArrayItems(value.output)
}

func deliveryString(record JSONOutput, field string, state evaluation) (string, error) {
	value, err := readOutputPointer(record, []string{field}, state.valueReader)
	if err != nil {
		return "", err
	}
	return readComparisonText(value)
}

func (declaration compiledDeliveryRange) readTexts(state evaluation) ([]deliveryTextObservation, error) {
	records, err := readDeliveryRecords(declaration.texts, state)
	if err != nil {
		return nil, err
	}
	observations := make([]deliveryTextObservation, len(records))
	seen := make(map[string]bool, len(records))
	for index, record := range records {
		observation := &observations[index]
		for _, field := range []struct {
			name   string
			output *string
		}{{"key", &observation.key}, {"text", &observation.text}, {"mode", &observation.mode}, {"path", &observation.path}} {
			*field.output, err = deliveryString(record, field.name, state)
			if err != nil {
				return nil, err
			}
		}
		if observation.key == "" || seen[observation.key] {
			return nil, deliveryRangeIssue(InvalidAssociation, observation.path, "delivery texts require unique nonempty keys within a frame")
		}
		seen[observation.key] = true
		if observation.mode != "append" && observation.mode != "snapshot" {
			return nil, deliveryRangeIssue(InvalidInput, observation.path, "delivery text mode must be append or snapshot")
		}
	}
	return observations, nil
}

func (declaration compiledDeliveryRange) readRanges(state evaluation) ([]deliveryRangeObservation, error) {
	records, err := readDeliveryRecords(declaration.ranges, state)
	if err != nil {
		return nil, err
	}
	observations := make([]deliveryRangeObservation, len(records))
	seen := make(map[[2]string]bool, len(records))
	for index, record := range records {
		observation := &observations[index]
		for _, field := range []struct {
			name   string
			output *string
		}{{"key", &observation.key}, {"id", &observation.id}, {"path", &observation.path}} {
			*field.output, err = deliveryString(record, field.name, state)
			if err != nil {
				return nil, err
			}
		}
		for _, field := range []struct {
			name   string
			output *int64
		}{{"start", &observation.start}, {"end", &observation.end}} {
			value, err := readOutputPointer(record, []string{field.name}, state.valueReader)
			if err != nil {
				return nil, err
			}
			*field.output, err = readPlannedInteger(value)
			if err != nil || *field.output < 0 {
				return nil, deliveryRangeIssue(InvalidInput, observation.path, "reference coordinates must be nonnegative integers")
			}
		}
		key := [2]string{observation.key, observation.id}
		if observation.key == "" || observation.id == "" || seen[key] || observation.start > observation.end {
			return nil, deliveryRangeIssue(InvalidAssociation, observation.path, "delivery references require unique identities and ordered coordinates")
		}
		seen[key] = true
	}
	return observations, nil
}

func (declaration compiledDeliveryRange) measureText(state evaluation, text string) (int64, error) {
	// The compiled primitive binds the common measure directly. Borrowing an
	// unescaped string never needs to re-encode a body solely to count its units.
	if declaration.measuresUTF16 {
		return int64(utf16TextLength(text)), state.ctx.Err()
	}
	state.input = StringValue(text)
	state.root = state.input
	state.inputView, state.objectReader = nil, nil
	state.inputReader, state.rootReader = nil, nil
	value, err := declaration.measure.build(state)
	if err != nil {
		return 0, err
	}
	length, err := readPlannedInteger(value.output)
	if err != nil || length < 0 {
		return 0, fmt.Errorf("%s: text measure must produce a nonnegative integer", declaration.measure.expression.path)
	}
	return length, nil
}
