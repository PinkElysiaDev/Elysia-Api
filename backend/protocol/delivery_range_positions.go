package protocol

import (
	"math"
	"sort"
	"unicode/utf8"
)

// deliveryTextPositions contains coordinate proofs, never the forwarded body.
// Until the first rewrite, the entire observed text is one implicit clean span.
type deliveryTextPositions struct {
	source, output int64
	diverged       bool
	spans          []streamPositionSpan
}

func (positions *deliveryTextPositions) append(before, after string, measure func(string) (int64, error)) error {
	sourceLength, err := measure(before)
	if err != nil {
		return err
	}
	outputLength := sourceLength
	if before != after {
		outputLength, err = measure(after)
		if err != nil {
			return err
		}
	}
	if sourceLength > math.MaxInt64-positions.source || outputLength > math.MaxInt64-positions.output {
		return deliveryRangeIssue(LimitExceeded, "/range", "delivery position exceeds integer range")
	}
	if before == after {
		if positions.diverged {
			positions.addSpan(positions.source, positions.output, sourceLength)
		}
	} else {
		if !positions.diverged {
			positions.addSpan(0, 0, positions.source)
			positions.diverged = true
		}
		prefix, suffix := unchangedTextEdges(before, after)
		prefixLength, err := measure(before[:prefix])
		if err != nil {
			return err
		}
		suffixLength, err := measure(before[len(before)-suffix:])
		if err != nil {
			return err
		}
		// Coordinates must compose across substring boundaries. A custom
		// measure that cannot establish this fact cannot establish this proof.
		if prefixLength > sourceLength-suffixLength || prefixLength > outputLength-suffixLength {
			return deliveryRangeIssue(InvalidInput, "/measure", "text measure is inconsistent across unchanged regions")
		}
		positions.addSpan(positions.source, positions.output, prefixLength)
		positions.addSpan(positions.source+sourceLength-suffixLength, positions.output+outputLength-suffixLength, suffixLength)
	}
	positions.source += sourceLength
	positions.output += outputLength
	return nil
}

func (positions *deliveryTextPositions) addSpan(source, output, length int64) {
	if length == 0 {
		return
	}
	if count := len(positions.spans); count != 0 {
		last := &positions.spans[count-1]
		if last.local+last.length == source && last.output+last.length == output {
			last.length += length
			return
		}
	}
	positions.spans = append(positions.spans, streamPositionSpan{local: source, output: output, length: length})
}

func (positions *deliveryTextPositions) project(start, end int64) (int64, int64, error) {
	if start < 0 || end < start || end > positions.source {
		return 0, 0, deliveryRangeIssue(InvalidAssociation, "/range", "reference exceeds observed source text")
	}
	if !positions.diverged {
		return start, end, nil
	}
	index := sort.Search(len(positions.spans), func(index int) bool {
		span := positions.spans[index]
		return span.local+span.length >= end
	})
	if index == len(positions.spans) || positions.spans[index].local > start {
		return 0, 0, deliveryRangeIssue(ConversionRejected, "/range", "reference intersects rewritten or unproven text")
	}
	span := positions.spans[index]
	offset := span.output - span.local
	return start + offset, end + offset, nil
}

// unchangedTextEdges compares only this increment. Unicode boundaries prevent
// a shared UTF-8 byte prefix from claiming part of a changed character.
func unchangedTextEdges(before, after string) (prefix, suffix int) {
	for prefix < len(before) && prefix < len(after) {
		left, leftSize := utf8.DecodeRuneInString(before[prefix:])
		right, rightSize := utf8.DecodeRuneInString(after[prefix:])
		if left != right || leftSize != rightSize {
			break
		}
		prefix += leftSize
	}
	for suffix < len(before)-prefix && suffix < len(after)-prefix {
		left, leftSize := utf8.DecodeLastRuneInString(before[:len(before)-suffix])
		right, rightSize := utf8.DecodeLastRuneInString(after[:len(after)-suffix])
		if left != right || leftSize != rightSize {
			break
		}
		suffix += leftSize
	}
	return prefix, suffix
}

func deliveryRangeIssue(code IssueCode, path, reason string) error {
	return &declaredRejectionError{diagnostic: RejectDiagnostic{Code: code, Stage: "wire", Path: path}, reason: reason}
}
