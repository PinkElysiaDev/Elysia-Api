package protocol

import (
	"cmp"
	"slices"
	"strings"
)

const (
	objectFieldSmallCapacity  = 4
	objectFieldInlineCapacity = 8
)

// Views are immutable after publication and live within their input owner.
type objectFieldView struct {
	fields []objectField
	mapped Object
}

type smallObjectFieldView struct {
	objectFieldView
	storage [objectFieldSmallCapacity]objectField
}

type mediumObjectFieldView struct {
	objectFieldView
	storage [objectFieldInlineCapacity]objectField
}

type objectFieldBuilder struct {
	fields [objectFieldInlineCapacity]objectField
	count  int
	mapped Object
}

func (value Value) readObjectView(reader *valueReader) (*objectFieldView, error) {
	var builder objectFieldBuilder
	if _, err := value.scanObjectFields(reader, &builder); err != nil {
		return nil, err
	}
	return builder.finish(), nil
}

func (value Value) readOutputFields(keys []string, isInclude bool) ([]jsonOutputField, error) {
	var builder objectFieldBuilder
	if _, err := value.scanObjectFields(&valueReader{}, &builder); err != nil {
		return nil, err
	}
	return builder.buildOutputFields(keys, isInclude), nil
}

// Selected keys own storage independently of the scanned input. Member values
// keep the existing immutable-leaf ownership after selection and normalization.
func (builder *objectFieldBuilder) buildOutputFields(keys []string, isInclude bool) []jsonOutputField {
	capacity := builder.count
	if builder.mapped != nil {
		capacity = len(builder.mapped)
	}
	if isInclude {
		capacity = min(capacity, len(keys))
	}
	selected := make([]jsonOutputField, 0, capacity)
	appendField := func(key string, value Value) {
		_, isSelected := slices.BinarySearch(keys, key)
		if isSelected != isInclude {
			return
		}
		value.raw = compactValueString(value.raw)
		selected = append(selected, jsonOutputField{strings.Clone(key), JSONLeaf(value)})
	}
	if builder.mapped != nil {
		for key, value := range builder.mapped {
			appendField(key, value)
		}
	} else {
		for _, field := range builder.fields[:builder.count] {
			appendField(field.key, field.value)
		}
	}
	slices.SortFunc(selected, func(left, right jsonOutputField) int { return cmp.Compare(left.key, right.key) })
	return selected
}

func (builder *objectFieldBuilder) addField(field objectField) {
	if builder.mapped != nil {
		builder.mapped[field.key] = field.value
		return
	}
	for index := range builder.count {
		if builder.fields[index].key == field.key {
			builder.fields[index].value = field.value
			return
		}
	}
	if builder.count < len(builder.fields) {
		builder.fields[builder.count] = field
		builder.count++
		return
	}
	builder.mapped = make(Object, objectFieldInlineCapacity+1)
	for _, previous := range builder.fields {
		builder.mapped[previous.key] = previous.value
	}
	builder.mapped[field.key] = field.value
}

func (builder *objectFieldBuilder) finish() *objectFieldView {
	if builder.mapped != nil || builder.count == 0 {
		return &objectFieldView{mapped: builder.mapped}
	}
	var view *objectFieldView
	if builder.count <= objectFieldSmallCapacity {
		storage := &smallObjectFieldView{}
		storage.fields = storage.storage[:builder.count]
		view = &storage.objectFieldView
	} else {
		storage := &mediumObjectFieldView{}
		storage.fields = storage.storage[:builder.count]
		view = &storage.objectFieldView
	}
	copy(view.fields, builder.fields[:builder.count])
	return view
}

func (view *objectFieldView) readMember(key string) (Value, bool) {
	if view.mapped != nil {
		value, exists := view.mapped[key]
		return value, exists
	}
	for _, field := range view.fields {
		if field.key == key {
			return field.value, true
		}
	}
	return Value{}, false
}
