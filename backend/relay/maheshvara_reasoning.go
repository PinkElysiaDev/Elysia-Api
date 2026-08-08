package relay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MaheshvaraProtocolVersion           = "1"
	maheshvaraReasoningEnvelopeV1       = "maheshvara-reasoning-v1:"
	maheshvaraReasoningMaxBytes         = 4 << 20
	geminiCrossProviderThoughtSignature = "skip_thought_signature_validator"
)

func canonicalSignatureForProvider(signature, sourceProvider, targetProvider string) string {
	if strings.TrimSpace(signature) == "" || !strings.EqualFold(strings.TrimSpace(sourceProvider), strings.TrimSpace(targetProvider)) {
		return ""
	}
	return signature
}

type maheshvaraReasoningEnvelope struct {
	Version          string                      `json:"version"`
	Text             string                      `json:"text,omitempty"`
	EncryptedContent string                      `json:"encrypted_content"`
	Summary          []CanonicalReasoningSummary `json:"summary,omitempty"`
}

func encodeMaheshvaraReasoningEnvelope(text, encryptedContent string, summary []CanonicalReasoningSummary) (string, error) {
	if strings.TrimSpace(encryptedContent) == "" {
		return "", nil
	}
	payload, err := json.Marshal(maheshvaraReasoningEnvelope{
		Version:          MaheshvaraProtocolVersion,
		Text:             text,
		EncryptedContent: encryptedContent,
		Summary:          append([]CanonicalReasoningSummary(nil), summary...),
	})
	if err != nil {
		return "", fmt.Errorf("encode Maheshvara reasoning envelope: %w", err)
	}
	if len(payload) > maheshvaraReasoningMaxBytes {
		return "", fmt.Errorf("Maheshvara reasoning envelope exceeds %d bytes", maheshvaraReasoningMaxBytes)
	}
	return maheshvaraReasoningEnvelopeV1 + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeMaheshvaraReasoningEnvelope(value string) (maheshvaraReasoningEnvelope, bool) {
	payload := strings.TrimPrefix(value, maheshvaraReasoningEnvelopeV1)
	if payload == value || payload == "" {
		return maheshvaraReasoningEnvelope{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(decoded) == 0 || len(decoded) > maheshvaraReasoningMaxBytes {
		return maheshvaraReasoningEnvelope{}, false
	}
	var envelope maheshvaraReasoningEnvelope
	if err := json.Unmarshal(decoded, &envelope); err != nil {
		return maheshvaraReasoningEnvelope{}, false
	}
	if envelope.Version != MaheshvaraProtocolVersion || strings.TrimSpace(envelope.EncryptedContent) == "" {
		return maheshvaraReasoningEnvelope{}, false
	}
	return envelope, true
}

func canonicalReasoningText(item CanonicalOutputItem) string {
	if item.Reasoning != nil {
		if item.Reasoning.Text != "" {
			return item.Reasoning.Text
		}
		if item.Reasoning.Summary != "" {
			return item.Reasoning.Summary
		}
	}
	var builder strings.Builder
	for _, part := range item.Content {
		if part.Type != CanonicalContentReasoning {
			continue
		}
		builder.WriteString(firstNonEmptyString(part.ReasoningText, part.Text))
	}
	if builder.Len() > 0 {
		return builder.String()
	}
	for _, summary := range item.Summary {
		builder.WriteString(summary.Text)
	}
	return builder.String()
}

func canonicalReasoningEncryptedContent(item CanonicalOutputItem) string {
	if item.Reasoning != nil && item.Reasoning.EncryptedContent != "" {
		return item.Reasoning.EncryptedContent
	}
	for _, part := range item.Content {
		if part.Type == CanonicalContentReasoning && part.EncryptedContent != "" {
			return part.EncryptedContent
		}
	}
	return ""
}

func canonicalReasoningSummary(item CanonicalOutputItem) []CanonicalReasoningSummary {
	if len(item.Summary) > 0 {
		return append([]CanonicalReasoningSummary(nil), item.Summary...)
	}
	if item.Reasoning != nil && len(item.Reasoning.SummaryParts) > 0 {
		return append([]CanonicalReasoningSummary(nil), item.Reasoning.SummaryParts...)
	}
	for _, part := range item.Content {
		if len(part.ReasoningSummary) > 0 {
			return append([]CanonicalReasoningSummary(nil), part.ReasoningSummary...)
		}
	}
	if text := canonicalReasoningText(item); text != "" {
		return []CanonicalReasoningSummary{{Type: "summary_text", Text: text}}
	}
	return nil
}
