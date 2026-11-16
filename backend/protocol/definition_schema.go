package protocol

import "github.com/invopop/jsonschema"

const compactReadPattern = `^\$((input|root|item|context|runtime|state)(/([^~]|~[01])*)?|binding/([^~/]|~[01])+(/([^~]|~[01])*)?)$`

// JSONSchemaExtend advertises the two authored forms of the same expression.
func (Expression) JSONSchemaExtend(schema *jsonschema.Schema) {
	object := *schema
	*schema = jsonschema.Schema{OneOf: []*jsonschema.Schema{
		&object,
		{Type: "string", Pattern: compactReadPattern,
			Description: "Compact optional read: $input/path, $root/path, $item/path, $context/path, $runtime/path, $state/slot/path, or $binding/escaped-name/path. Paths and binding names use RFC 6901 escaping. Scope permissions are checked during compilation."},
	}}
}
