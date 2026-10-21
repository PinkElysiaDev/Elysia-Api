package protocol

import "sync"

// Large borrowed subtrees are the ones expensive to rescan and useful to index.
// One sequential structural scan records their ends, without materializing
// objects or per-byte entries. Small/copied fragments keep the direct scanner.
// Limits bound both metadata and parser stack independently of authored budgets;
// exceeding them discards the optional index and keeps the established reader.
const valueIndexEntries = 4096

type valueIndex struct {
	root         string
	minimumBytes int
	once         sync.Once
	ends         map[*byte]int
}

func (reader *valueReader) valueEnd(raw string, start int) int {
	if reader != nil && reader.index != nil && len(raw) >= reader.minimumSharedBytes {
		index := reader.index
		index.once.Do(index.indexLargeSpans)
		if length := index.ends[spanIdentity(raw[start:]).data]; length != 0 && length <= len(raw)-start {
			return start + length
		}
	}
	return jsonValueEnd(raw, start)
}

func (index *valueIndex) indexLargeSpans() {
	raw := index.root
	var stack [typedMeterTraversalDepth]int
	depth := 0
	ends := make(map[*byte]int)
	add := func(start, end int) bool {
		if end-start < index.minimumBytes {
			return true
		}
		if len(ends) >= valueIndexEntries {
			return false
		}
		ends[spanIdentity(raw[start:end]).data] = end - start
		return true
	}
	for offset := 0; offset < len(raw); {
		switch raw[offset] {
		case '"':
			end := jsonStringEnd(raw, offset)
			if !add(offset, end) {
				return
			}
			offset = end
		case '{', '[':
			if depth == len(stack) {
				return
			}
			stack[depth] = offset
			depth++
			offset++
		case '}', ']':
			depth--
			offset++
			if !add(stack[depth], offset) {
				return
			}
		default:
			offset++
		}
	}
	index.ends = ends
}
