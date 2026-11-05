package protocol

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"math"
	"strconv"
	"strings"
)

const streamDigestScratchBytes = 1024

const (
	streamDigestNull byte = iota
	streamDigestFalse
	streamDigestTrue
	streamDigestString
	streamDigestNumber
	streamDigestArray
	streamDigestObject
)

// streamArrayTracker retains only the semantic digest of the delivered prefix.
// Fixed-size element digests frame the chain without retaining encoded values.
type streamArrayTracker struct {
	digest [sha256.Size]byte
	count  int
}

func (tracker streamArrayTracker) update(ctx context.Context, items []JSONOutput, isSnapshot bool) (streamArrayTracker, int, error) {
	start, next := 0, tracker
	if isSnapshot {
		if len(items) < tracker.count {
			return tracker, 0, streamIssue(UpstreamContractViolation, "/array", "array snapshot shortened its delivered prefix")
		}
		start, next = tracker.count, streamArrayTracker{}
	} else if len(items) > math.MaxInt-tracker.count {
		return tracker, 0, streamIssue(LimitExceeded, "/array", "array item count exceeds its integer range")
	}
	hasher := streamValueHasher{ctx: ctx, digest: sha256.New()}
	for index, item := range items {
		digest, err := hasher.sum(item)
		if err != nil {
			return tracker, 0, err
		}
		next.append(digest)
		if isSnapshot && index+1 == start && next.digest != tracker.digest {
			return tracker, 0, streamIssue(UpstreamContractViolation, "/array", "array snapshot changed its delivered prefix")
		}
	}
	return next, start, nil
}

func (tracker *streamArrayTracker) append(digest [sha256.Size]byte) {
	var pair [2 * sha256.Size]byte
	copy(pair[:sha256.Size], tracker.digest[:])
	copy(pair[sha256.Size:], digest[:])
	tracker.digest = sha256.Sum256(pair[:])
	tracker.count++
}

// The reader borrows only within this hash operation. Neither parsed fields nor
// the hash workspace enter retained stream state.
type streamValueHasher struct {
	ctx     context.Context
	digest  hash.Hash
	reader  valueReader
	scratch [streamDigestScratchBytes]byte
}

func (hasher *streamValueHasher) sum(output JSONOutput) ([sha256.Size]byte, error) {
	hasher.digest.Reset()
	var digest [sha256.Size]byte
	if err := hasher.writeValue(output); err != nil {
		return digest, err
	}
	hasher.digest.Sum(digest[:0])
	return digest, nil
}

func (hasher *streamValueHasher) writeValue(output JSONOutput) error {
	if err := hasher.ctx.Err(); err != nil {
		return err
	}
	if output.node != nil {
		if output.node.object {
			fields, err := plannedObjectFields(output)
			if err != nil {
				return err
			}
			hasher.writeTag(streamDigestObject)
			hasher.writeLength(len(fields))
			for _, field := range fields {
				if err := hasher.writeString(field.key); err != nil {
					return err
				}
				if err := hasher.writeValue(field.value); err != nil {
					return err
				}
			}
			return nil
		}
		return hasher.writeArray(output.node.items)
	}
	if output.value.IsZero() {
		return fmt.Errorf("array tracking requires present JSON members")
	}
	raw := strings.TrimSpace(output.value.raw)
	switch raw[0] {
	case '{':
		return hasher.writeObject(output.value)
	case '[':
		items, err := output.value.readArrayWithin(&hasher.reader)
		if err != nil {
			return err
		}
		hasher.writeTag(streamDigestArray)
		hasher.writeLength(len(items))
		for _, item := range items {
			if err := hasher.writeValue(JSONLeaf(item)); err != nil {
				return err
			}
		}
		return nil
	case '"':
		text, err := readString(output.value)
		if err != nil {
			return err
		}
		hasher.writeTag(streamDigestString)
		return hasher.writeString(text)
	case 'n':
		hasher.writeTag(streamDigestNull)
	case 'f':
		hasher.writeTag(streamDigestFalse)
	case 't':
		hasher.writeTag(streamDigestTrue)
	default:
		hasher.writeTag(streamDigestNumber)
		coefficient, exponent := normalizeStreamNumber(raw)
		if err := hasher.writeString(coefficient); err != nil {
			return err
		}
		return hasher.writeString(exponent)
	}
	return nil
}

func (hasher *streamValueHasher) writeObject(value Value) error {
	fields, err := value.readObjectWithin(&hasher.reader)
	if err != nil {
		return err
	}
	hasher.writeTag(streamDigestObject)
	hasher.writeLength(len(fields))
	for _, key := range sortedKeys(fields) {
		if err := hasher.writeString(key); err != nil {
			return err
		}
		if err := hasher.writeValue(JSONLeaf(fields[key])); err != nil {
			return err
		}
	}
	return nil
}

func (hasher *streamValueHasher) writeArray(items []JSONOutput) error {
	hasher.writeTag(streamDigestArray)
	hasher.writeLength(len(items))
	for _, item := range items {
		if err := hasher.writeValue(item); err != nil {
			return err
		}
	}
	return nil
}

func (hasher *streamValueHasher) writeTag(tag byte) {
	hasher.scratch[0] = tag
	_, _ = hasher.digest.Write(hasher.scratch[:1])
}

func (hasher *streamValueHasher) writeLength(length int) {
	binary.LittleEndian.PutUint64(hasher.scratch[:8], uint64(length))
	_, _ = hasher.digest.Write(hasher.scratch[:8])
}

func (hasher *streamValueHasher) writeString(text string) error {
	hasher.writeLength(len(text))
	for len(text) != 0 {
		if err := hasher.ctx.Err(); err != nil {
			return err
		}
		count := copy(hasher.scratch[:], text)
		_, _ = hasher.digest.Write(hasher.scratch[:count])
		text = text[count:]
	}
	return nil
}

// Decimal spelling is normalized without expanding powers of ten. Even an
// exponent longer than a machine integer costs only its input length.
func normalizeStreamNumber(raw string) (string, string) {
	isNegative := raw[0] == '-'
	if isNegative {
		raw = raw[1:]
	}
	exponent := "0"
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		raw, exponent = raw[:index], raw[index+1:]
	}
	fractionDigits := 0
	if index := strings.IndexByte(raw, '.'); index >= 0 {
		fractionDigits = len(raw) - index - 1
	}
	first, last := -1, -1
	for index := range len(raw) {
		if raw[index] != '0' && raw[index] != '.' {
			if first < 0 {
				first = index
			}
			last = index
		}
	}
	if first < 0 {
		return "0", "0"
	}
	trailingZeros := len(raw) - last - 1
	if strings.IndexByte(raw[last+1:], '.') >= 0 {
		trailingZeros--
	}
	coefficient := strings.ReplaceAll(raw[first:last+1], ".", "")
	if isNegative {
		coefficient = "-" + coefficient
	}
	return coefficient, shiftDecimalExponent(exponent, trailingZeros-fractionDigits)
}

func shiftDecimalExponent(exponent string, shift int) string {
	isNegative := exponent[0] == '-'
	if exponent[0] == '-' || exponent[0] == '+' {
		exponent = exponent[1:]
	}
	exponent = strings.TrimLeft(exponent, "0")
	if exponent == "" {
		return strconv.Itoa(shift)
	}
	if shift == 0 {
		return signedDecimal(exponent, isNegative)
	}
	adjustment := strconv.Itoa(shift)
	isAdjustmentNegative := adjustment[0] == '-'
	if isAdjustmentNegative {
		adjustment = adjustment[1:]
	}
	if isNegative == isAdjustmentNegative {
		return signedDecimal(addDecimalDigits(exponent, adjustment), isNegative)
	}
	if len(exponent) < len(adjustment) || len(exponent) == len(adjustment) && exponent < adjustment {
		exponent, adjustment, isNegative = adjustment, exponent, isAdjustmentNegative
	}
	return signedDecimal(subtractDecimalDigits(exponent, adjustment), isNegative)
}

func signedDecimal(digits string, isNegative bool) string {
	if digits == "" || digits == "0" {
		return "0"
	}
	if isNegative {
		return "-" + digits
	}
	return digits
}

func addDecimalDigits(left, right string) string {
	output := make([]byte, max(len(left), len(right))+1)
	carry, a, b := 0, len(left)-1, len(right)-1
	for position := len(output) - 1; position >= 0; position-- {
		value := carry
		if a >= 0 {
			value += int(left[a] - '0')
			a--
		}
		if b >= 0 {
			value += int(right[b] - '0')
			b--
		}
		output[position], carry = byte(value%10)+'0', value/10
	}
	return strings.TrimLeft(string(output), "0")
}

func subtractDecimalDigits(left, right string) string {
	output := make([]byte, len(left))
	borrow, b := 0, len(right)-1
	for position := len(output) - 1; position >= 0; position-- {
		value := int(left[position]-'0') - borrow
		if b >= 0 {
			value -= int(right[b] - '0')
			b--
		}
		borrow = 0
		if value < 0 {
			value += 10
			borrow = 1
		}
		output[position] = byte(value) + '0'
	}
	return strings.TrimLeft(string(output), "0")
}
