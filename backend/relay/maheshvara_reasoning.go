package relay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// 推理走私信封：跨协议转换（如 Responses↔Claude）时，目标协议没有承载
// 「加密思考」的原生字段，把密文装进信封存放在协议的签名/数据槽位里，
// 下轮请求回来时再解出回放——多轮任务中模型得以延续上一轮的思考。
const (
	// MaheshvaraProtocolVersion 是信封 v2 的版本号；v1 信封保持只读兼容
	// （无 provider/model 门控信息）。
	MaheshvaraProtocolVersion           = "2"
	maheshvaraReasoningEnvelopeV1       = "maheshvara-reasoning-v1:"
	maheshvaraReasoningEnvelopeV2       = "maheshvara-reasoning-v2:"
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
	// Provider/Model 记录密文签发方与签发时模型（v2 新增）：回放时按 provider
	// 门控，密文只发还给同厂商上游，不匹配则丢弃密文只保留明文/摘要。
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

func encodeMaheshvaraReasoningEnvelope(text, encryptedContent string, summary []CanonicalReasoningSummary, provider, model string) (string, error) {
	if strings.TrimSpace(encryptedContent) == "" {
		return "", nil
	}
	payload, err := json.Marshal(maheshvaraReasoningEnvelope{
		Version:          MaheshvaraProtocolVersion,
		Text:             text,
		EncryptedContent: encryptedContent,
		Summary:          append([]CanonicalReasoningSummary(nil), summary...),
		Provider:         provider,
		Model:            model,
	})
	if err != nil {
		return "", fmt.Errorf("encode Maheshvara reasoning envelope: %w", err)
	}
	if len(payload) > maheshvaraReasoningMaxBytes {
		return "", fmt.Errorf("Maheshvara reasoning envelope exceeds %d bytes", maheshvaraReasoningMaxBytes)
	}
	return maheshvaraReasoningEnvelopeV2 + base64.RawURLEncoding.EncodeToString(payload), nil
}

// decodeMaheshvaraReasoningEnvelope 解析 v2/v1 信封。v1（无 provider 信息）
// 仅保持只读兼容，出站一律写 v2。
func decodeMaheshvaraReasoningEnvelope(value string) (maheshvaraReasoningEnvelope, bool) {
	version := ""
	payload := ""
	if strings.HasPrefix(value, maheshvaraReasoningEnvelopeV2) {
		version = "2"
		payload = strings.TrimPrefix(value, maheshvaraReasoningEnvelopeV2)
	} else if strings.HasPrefix(value, maheshvaraReasoningEnvelopeV1) {
		version = "1"
		payload = strings.TrimPrefix(value, maheshvaraReasoningEnvelopeV1)
	}
	if payload == "" {
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
	if envelope.Version != version || strings.TrimSpace(envelope.EncryptedContent) == "" {
		return maheshvaraReasoningEnvelope{}, false
	}
	return envelope, true
}

// claudeThinkingSignatureForPart 计算推理 part 走 Claude 线时的签名槽位值：
//   - 厂商原生签名（provider 匹配 anthropic）原样使用；
//   - 携带密文的跨协议思考装进信封（v2，随载签发方与模型），客户端原样回传后
//     解出回放；签发方非 anthropic 时密文绝不以原生形态发给 Claude 上游。
func claudeThinkingSignatureForPart(part CanonicalContentPart, model string) string {
	if signature := canonicalSignatureForProvider(part.Signature, part.SignatureProvider, CanonicalSignatureProviderAnthropic); signature != "" {
		return signature
	}
	if strings.TrimSpace(part.EncryptedContent) == "" {
		return ""
	}
	if part.EncryptedProvider == CanonicalSignatureProviderAnthropic {
		// anthropic 的"密文"就是 thinking 签名本身，无需再包信封。
		return part.EncryptedContent
	}
	signature, err := encodeMaheshvaraReasoningEnvelope(
		firstNonEmptyString(part.ReasoningText, part.Text),
		part.EncryptedContent,
		part.ReasoningSummary,
		firstNonEmptyString(part.EncryptedProvider, part.SignatureProvider),
		firstNonEmptyString(part.EncryptedModel, model),
	)
	if err != nil {
		return ""
	}
	return signature
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
