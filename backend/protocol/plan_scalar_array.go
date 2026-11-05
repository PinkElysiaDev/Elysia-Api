package protocol

import (
	"fmt"
	"strings"
)

// Only immediate scalar consumers use this reader. Borrowed members never enter
// retained containers; join owns its output before the next member is visited.
type scalarArrayReader struct {
	raw    string
	items  []JSONOutput
	offset int
}

func newScalarArrayReader(output JSONOutput) (scalarArrayReader, error) {
	if output.node != nil {
		if output.node.object {
			return scalarArrayReader{}, fmt.Errorf("expected an array")
		}
		return scalarArrayReader{items: output.node.items}, nil
	}
	if !output.value.IsArray() {
		return scalarArrayReader{}, fmt.Errorf("expected an array")
	}
	raw := strings.TrimSpace(output.value.raw)
	return scalarArrayReader{raw: raw, offset: skipJSONSpace(raw, 1)}, nil
}

func (reader *scalarArrayReader) next() (JSONOutput, bool) {
	if reader.raw == "" {
		if reader.offset == len(reader.items) {
			return JSONOutput{}, false
		}
		item := reader.items[reader.offset]
		reader.offset++
		return item, true
	}
	if reader.raw[reader.offset] == ']' {
		return JSONOutput{}, false
	}
	end := jsonValueEnd(reader.raw, reader.offset)
	item := JSONLeaf(Value{raw: reader.raw[reader.offset:end]})
	reader.offset = skipJSONSpace(reader.raw, end)
	if reader.raw[reader.offset] == ',' {
		reader.offset = skipJSONSpace(reader.raw, reader.offset+1)
	}
	return item, true
}

func countPlannedArray(output JSONOutput) (int, error) {
	if output.node != nil {
		if output.node.object {
			return 0, fmt.Errorf("expected an array")
		}
		return len(output.node.items), nil
	}
	reader, err := newScalarArrayReader(output)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, ok := reader.next(); ok; _, ok = reader.next() {
		count++
	}
	return count, nil
}
