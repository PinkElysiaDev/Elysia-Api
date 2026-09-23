// Maheshvara 转换中请求入/出两侧共享的小助手（角色规范化、推理档位映射、数据 URL 等）。
package relay

import "strings"

// normalizeMaheshvaraRole 统一消息角色：小写化去空白；system/developer 类
// 角色在目标线制里通常单独承载（system 块/systemInstruction/instructions），
// 返回 isSystem 供各整形器跳过或折叠；tool/function 折叠为 user（Claude/
// Gemini 线制无 tool 角色）。
func normalizeMaheshvaraRole(role string) (normalized string, isSystem bool) {
	normalized = strings.ToLower(strings.TrimSpace(role))
	switch normalized {
	case "system", "developer":
		return normalized, true
	case "tool", "function":
		return "user", false
	}
	return normalized, false
}

// applyEnvelope 把 Maheshvara 思考密文信封的字段填进内容部件：签名让位
// 给密文形态（同线按密文回放，跨线按 provider 门控），Text 空时回填信封内
// 的明文思考。thinking/redacted 两类块的共同尾部。
func (part *MaheshvaraContentPart) applyEnvelope(envelope maheshvaraReasoningEnvelope) {
	part.Signature = ""
	part.SignatureProvider = MaheshvaraSignatureProviderMaheshvara
	part.EncryptedContent = envelope.EncryptedContent
	part.EncryptedProvider = envelope.Provider
	part.EncryptedModel = envelope.Model
	part.ReasoningSummary = envelope.Summary
	if part.Text == "" {
		part.Text = envelope.Text
		part.ReasoningText = envelope.Text
	}
}

// parseDataURL 解析 data:[<mediatype>][;base64],<data> URI，返回媒体类型与原始数据。
func parseDataURL(u string) (mediaType, data string, ok bool) {
	if !strings.HasPrefix(u, "data:") {
		return "", "", false
	}
	rest := u[len("data:"):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", "", false
	}
	meta := strings.TrimSuffix(rest[:comma], ";base64")
	return meta, rest[comma+1:], true
}

// effortFromBudget 把固定思考预算量化为 effort 档位（对齐 Claude 官方
// budget 档：1024/4096/16384，超过 high 归 xhigh）。
func effortFromBudget(budget int) string {
	if budget <= 0 {
		return ""
	}
	if budget <= effortBudgetLow {
		return "low"
	}
	if budget <= effortBudgetMedium {
		return "medium"
	}
	if budget <= effortBudgetHigh {
		return "high"
	}
	return "xhigh"
}

// budgetFromEffort 把 effort 档位量化回固定预算（low/medium/high/xhigh →
// Claude 官方档位预算；xhigh 与 max 共享 32000 上限）。
func budgetFromEffort(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "minimal", "min":
		return effortBudgetLow
	case "medium":
		return effortBudgetMedium
	case "high":
		return effortBudgetHigh
	case "xhigh", "max":
		return effortBudgetMax
	default:
		return EffortBudgetDefault
	}
}
