package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// decodeWireJSON accepts one document and retains integers in interface fields.
func decodeWireJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}
