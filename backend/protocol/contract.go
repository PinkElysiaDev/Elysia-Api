package protocol

import (
	"fmt"

	"github.com/bytedance/sonic/decoder"
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
	reader := decoder.NewDecoder(string(raw))
	reader.DisallowUnknownFields()
	reader.UseNumber()
	reader.CopyString()
	reader.ValidateString()
	if err := reader.Decode(target); err != nil {
		return err
	}
	if err := reader.CheckTrailings(); err != nil {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}
