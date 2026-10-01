package protocol

import (
	"reflect"
	"strings"
)

// SchemaCatalog is the shared machine-readable contract for editor, Agent and
// API clients. The JSON schema is generated from the actual strict Go model.
type SchemaCatalog struct {
	SchemaVersion   int             `json:"schemaVersion"`
	CompilerVersion string          `json:"compilerVersion"`
	Directions      []Direction     `json:"directions"`
	Capabilities    []Capability    `json:"capabilities"`
	Operations      []OperationInfo `json:"mappingOperations"`
	Definition      map[string]any  `json:"definitionSchema"`
	Limits          Limits          `json:"limits"`
}

// DescribeSchema returns fresh schema/catalog objects without registry state.
func DescribeSchema() SchemaCatalog {
	definitions := make(map[string]any)
	root := schemaForType(reflect.TypeFor[Definition](), definitions)
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$defs"] = definitions
	return SchemaCatalog{SchemaVersion: DefinitionSchemaVersion, CompilerVersion: CompilerVersion, Directions: DirectionCatalog(), Capabilities: CapabilityCatalog(), Operations: ExpressionCatalog(), Definition: root, Limits: DefaultLimits()}
}

func schemaForType(kind reflect.Type, definitions map[string]any) map[string]any {
	if kind == reflect.TypeFor[Value]() {
		return map[string]any{}
	}
	if kind.Kind() == reflect.Pointer {
		return schemaForType(kind.Elem(), definitions)
	}
	switch kind.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaForType(kind.Elem(), definitions)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaForType(kind.Elem(), definitions)}
	case reflect.Struct:
		name := kind.Name()
		if _, exists := definitions[name]; !exists {
			definitions[name] = map[string]any{}
			properties := make(map[string]any)
			required := []string{}
			for index := 0; index < kind.NumField(); index++ {
				field := kind.Field(index)
				if !field.IsExported() {
					continue
				}
				tag := strings.Split(field.Tag.Get("json"), ",")
				if tag[0] == "-" {
					continue
				}
				fieldName := tag[0]
				if fieldName == "" {
					fieldName = field.Name
				}
				properties[fieldName] = schemaForType(field.Type, definitions)
				if len(tag) == 1 {
					required = append(required, fieldName)
				}
			}
			definitions[name] = map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	}
	return map[string]any{}
}
