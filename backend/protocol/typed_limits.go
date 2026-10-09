package protocol

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A bounded structural walk proves the successful resource-check path without
// allocating a JSON copy. Failures and types outside the closed semantic model
// use the encoder, retaining its error precedence, cycle detection and wording.
const typedMeterTraversalDepth = 256

type typedMeter struct {
	limits              Limits
	bytes, nodes, depth int
}

type typedField struct {
	index               int
	keyBytes            int
	omitEmpty, omitZero bool
	plan                *typedPlan
}

type typedPlan struct {
	kind        reflect.Kind
	isJSON      bool
	isSupported bool
	fields      []typedField
	element     *typedPlan
}

var typedLimitPlans = buildTypedLimitPlans()

// Plans are built once for fixed semantic roots, then are read-only. Every
// exported field participates, so adding a model field cannot evade metering.
func buildTypedLimitPlans() map[reflect.Type]*typedPlan {
	plans := map[reflect.Type]*typedPlan{}
	jsonType := reflect.TypeFor[Value]()
	marshaler := reflect.TypeFor[json.Marshaler]()
	var build func(reflect.Type) *typedPlan
	build = func(kind reflect.Type) *typedPlan {
		if plan := plans[kind]; plan != nil {
			return plan
		}
		plan := &typedPlan{kind: kind.Kind(), isSupported: true}
		plans[kind] = plan
		if kind == jsonType {
			plan.isJSON = true
			return plan
		}
		if kind.Implements(marshaler) || reflect.PointerTo(kind).Implements(marshaler) {
			plan.isSupported = false
			return plan
		}
		switch kind.Kind() {
		case reflect.Pointer, reflect.Slice:
			plan.element = build(kind.Elem())
		case reflect.Map:
			plan.isSupported = kind.Key().Kind() == reflect.String
			plan.element = build(kind.Elem())
		case reflect.Struct:
			for index := range kind.NumField() {
				field := kind.Field(index)
				if !field.IsExported() {
					continue
				}
				tag := strings.Split(field.Tag.Get("json"), ",")
				if tag[0] == "-" {
					continue
				}
				if field.Anonymous || tag[0] == "" {
					plan.isSupported = false
					continue
				}
				entry := typedField{index: index, keyBytes: jsonStringBytes(tag[0]), plan: build(field.Type)}
				for _, option := range tag[1:] {
					switch option {
					case "omitempty":
						entry.omitEmpty = true
					case "omitzero":
						entry.omitZero = true
					default:
						plan.isSupported = false
					}
				}
				plan.fields = append(plan.fields, entry)
			}
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int64:
		default:
			plan.isSupported = false
		}
		return plan
	}
	rootPlans := map[reflect.Type]*typedPlan{}
	for _, root := range []reflect.Type{reflect.TypeFor[*Request](), reflect.TypeFor[*Response](), reflect.TypeFor[[]Event]()} {
		rootPlans[root] = build(root)
	}
	return rootPlans
}

func fitsTypedLimits(value any, limits Limits) bool {
	plan := typedLimitPlans[reflect.TypeOf(value)]
	if plan == nil {
		return false
	}
	meter := typedMeter{limits: limits}
	return meter.visit(plan, reflect.ValueOf(value)) && meter.fits()
}

func (meter *typedMeter) fits() bool {
	return meter.bytes <= meter.limits.BufferBytes && meter.nodes <= meter.limits.Nodes && meter.depth <= meter.limits.Depth && meter.depth <= typedMeterTraversalDepth
}

func (meter *typedMeter) scalar(bytes int) bool {
	meter.bytes += bytes
	meter.nodes++
	return meter.fits()
}

func (meter *typedMeter) open() bool {
	meter.depth++
	return meter.scalar(1)
}

func (meter *typedMeter) close() bool {
	meter.depth--
	return meter.scalar(1)
}

func (meter *typedMeter) visit(plan *typedPlan, value reflect.Value) bool {
	if !plan.isSupported {
		return false
	}
	if plan.isJSON {
		return meter.raw(value.Field(0).String())
	}
	switch plan.kind {
	case reflect.Pointer:
		if value.IsNil() {
			return meter.scalar(4)
		}
		return meter.visit(plan.element, value.Elem())
	case reflect.String:
		return meter.scalar(jsonStringBytes(value.String()))
	case reflect.Bool:
		if value.Bool() {
			return meter.scalar(4)
		}
		return meter.scalar(5)
	case reflect.Int, reflect.Int64:
		var buffer [20]byte
		return meter.scalar(len(strconv.AppendInt(buffer[:0], value.Int(), 10)))
	case reflect.Struct:
		if !meter.open() {
			return false
		}
		count := 0
		for _, field := range plan.fields {
			child := value.Field(field.index)
			if field.omitZero && child.IsZero() || field.omitEmpty && isEmptyJSONField(child) {
				continue
			}
			if count > 0 {
				meter.bytes++
			}
			count++
			meter.bytes++ // colon; the field name is a JSON token as well.
			if !meter.scalar(field.keyBytes) || !meter.visit(field.plan, child) {
				return false
			}
		}
		return meter.close()
	case reflect.Slice:
		if value.IsNil() {
			return meter.scalar(4)
		}
		if !meter.open() {
			return false
		}
		for index := range value.Len() {
			if index > 0 {
				meter.bytes++
			}
			if !meter.visit(plan.element, value.Index(index)) {
				return false
			}
		}
		return meter.close()
	case reflect.Map:
		if value.IsNil() {
			return meter.scalar(4)
		}
		if !meter.open() {
			return false
		}
		iter := value.MapRange()
		count := 0
		for iter.Next() {
			if count > 0 {
				meter.bytes++
			}
			count++
			meter.bytes++
			if !meter.scalar(jsonStringBytes(iter.Key().String())) || !meter.visit(plan.element, iter.Value()) {
				return false
			}
		}
		return meter.close()
	}
	return false
}

func isEmptyJSONField(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Slice, reflect.Map, reflect.String:
		return value.Len() == 0
	case reflect.Pointer, reflect.Bool, reflect.Int, reflect.Int64:
		return value.IsZero()
	}
	return false
}

// raw measures encoding/json's compact, HTML-escaped Marshaler output. Value
// has already validated JSON; this walk never decodes numbers through float64.
func (meter *typedMeter) raw(raw string) bool {
	if raw == "" {
		return false
	}
	for offset := 0; offset < len(raw); {
		switch raw[offset] {
		case ' ', '\t', '\r', '\n':
			offset++
		case ',', ':':
			meter.bytes++
			offset++
		case '{', '[':
			if !meter.open() {
				return false
			}
			offset++
		case '}', ']':
			if !meter.close() {
				return false
			}
			offset++
		case '"':
			end := jsonStringEnd(raw, offset)
			length := end - offset
			for index := offset + 1; index < end-1; index++ {
				switch raw[index] {
				case '\\':
					index++
				case '<', '>', '&':
					length += 5
				case 0xe2:
					if index+2 < end && raw[index+1] == 0x80 && (raw[index+2] == 0xa8 || raw[index+2] == 0xa9) {
						length += 3
						index += 2
					}
				}
			}
			if !meter.scalar(length) {
				return false
			}
			offset = end
		default:
			end := jsonValueEnd(raw, offset)
			if !meter.scalar(end - offset) {
				return false
			}
			offset = end
		}
	}
	return meter.fits()
}

func jsonStringBytes(value string) int {
	if !utf8.ValidString(value) {
		raw, _ := json.Marshal(value)
		return len(raw)
	}
	length := 2
	for offset := 0; offset < len(value); {
		character := value[offset]
		if character >= utf8.RuneSelf {
			runeValue, width := utf8.DecodeRuneInString(value[offset:])
			if width == 1 || runeValue == '\u2028' || runeValue == '\u2029' {
				length += 6
			} else {
				length += width
			}
			offset += width
			continue
		}
		switch character {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			length += 2
		case '<', '>', '&':
			length += 6
		default:
			if character < 0x20 {
				length += 6
			} else {
				length++
			}
		}
		offset++
	}
	return length
}
