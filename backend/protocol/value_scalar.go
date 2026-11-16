package protocol

import (
	"strconv"
	"strings"
)

// Only unambiguous complete success is fast. This is not a second JSON error
// parser: all other cases use the existing decoder, preserving its acceptance,
// error type/message/offset and destination mutations, including malformed JSON.
func decodeScalarValue(raw string, target any) bool {
	text := strings.Trim(raw, " \t\r\n")
	switch output := target.(type) {
	case *string:
		if output == nil {
			return false
		}
		if text == "null" {
			return true
		}
		if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
			return false
		}
		body := text[1 : len(text)-1]
		for index := range len(body) {
			character := body[index]
			// Escapes and Unicode retain the decoder's surrogate/invalid-byte
			// policies. ASCII covers the short semantic enums and role names.
			if character < 0x20 || character >= 0x80 || character == '\\' || character == '"' {
				return false
			}
		}
		*output = strings.Clone(body)
		return true
	case *bool:
		if output == nil {
			return false
		}
		switch text {
		case "null":
			return true
		case "true":
			*output = true
			return true
		case "false":
			*output = false
			return true
		}
	case *int:
		if output == nil {
			return false
		}
		if text == "null" {
			return true
		}
		if isJSONInteger(text) {
			if number, err := strconv.ParseInt(text, 10, strconv.IntSize); err == nil {
				*output = int(number)
				return true
			}
		}
	case *int64:
		if output == nil {
			return false
		}
		if text == "null" {
			return true
		}
		if isJSONInteger(text) {
			if number, err := strconv.ParseInt(text, 10, 64); err == nil {
				*output = number
				return true
			}
		}
	case *uint64:
		if output == nil {
			return false
		}
		if text == "null" {
			return true
		}
		if isJSONInteger(text) && text[0] != '-' {
			if number, err := strconv.ParseUint(text, 10, 64); err == nil {
				*output = number
				return true
			}
		}
	}
	return false
}

func isJSONInteger(text string) bool {
	if text == "" {
		return false
	}
	if text[0] == '-' {
		text = text[1:]
	}
	if text == "" || len(text) > 1 && text[0] == '0' {
		return false
	}
	for index := range len(text) {
		if text[index] < '0' || text[index] > '9' {
			return false
		}
	}
	return true
}
