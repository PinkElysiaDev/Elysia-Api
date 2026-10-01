package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeRequestContract accepts one versioned semantic request. Unknown fields
// are rejected; wire extensions belong in Native and declared Attributes.
func DecodeRequestContract(raw []byte) (*Request, error) {
	var request Request
	if err := decodeContract(raw, &request); err != nil {
		return nil, err
	}
	if request.SchemaVersion != SemanticSchemaVersion {
		return nil, fmt.Errorf("unsupported semantic schemaVersion %d", request.SchemaVersion)
	}
	return &request, nil
}

func decodeContract(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}
