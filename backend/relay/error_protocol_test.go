package relay

import (
	"encoding/json"
	"testing"
)

// 四线制错误渲染矩阵:分类 → HTTP 状态 + 各协议标准体的关键字段。
// 规范依据见 error_protocol.go 头注(2026-09 官方文档与一手实测核验)。
func TestProtocolErrorBodyMatrix(t *testing.T) {
	cases := []struct {
		class         ErrorClass
		status        int // 期望状态(err.Status 为 0 时按分类推导)
		openAIType    string
		openAICode    string
		openAIParam   any
		anthropicType string
		anthropicStat int
		geminiStatus  string
	}{
		{ErrorClassInvalidRequest, 400, "invalid_request_error", "", nil, "invalid_request_error", 400, "INVALID_ARGUMENT"},
		{ErrorClassAuthentication, 401, "invalid_request_error", "invalid_api_key", nil, "authentication_error", 401, "UNAUTHENTICATED"},
		{ErrorClassPermission, 403, "invalid_request_error", "", nil, "permission_error", 403, "PERMISSION_DENIED"},
		{ErrorClassModelNotFound, 404, "invalid_request_error", "model_not_found", "model", "not_found_error", 404, "NOT_FOUND"},
		{ErrorClassRateLimit, 429, "rate_limit_error", "", nil, "rate_limit_error", 429, "RESOURCE_EXHAUSTED"},
		{ErrorClassOverloaded, 503, "service_unavailable_error", "server_is_overloaded", nil, "overloaded_error", 529, "UNAVAILABLE"},
		{ErrorClassUpstream, 502, "api_error", "", nil, "api_error", 502, "UNAVAILABLE"},
		{ErrorClassServer, 500, "api_error", "server_error", nil, "api_error", 500, "INTERNAL"},
	}
	for _, tc := range cases {
		for format, check := range map[FormatType]func(t *testing.T, status int, body []byte){
			FormatOpenAI: func(t *testing.T, status int, body []byte) {
				if status != tc.status {
					t.Fatalf("%s openai status = %d, want %d", tc.class, status, tc.status)
				}
				var parsed struct {
					Error struct {
						Message string `json:"message"`
						Type    string `json:"type"`
						Param   *string `json:"param"`
						Code    *string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(body, &parsed); err != nil {
					t.Fatalf("%s openai body: %v", tc.class, err)
				}
				// 四字段 required:param/code 为 null 而非缺席。
				if parsed.Error.Type != tc.openAIType || parsed.Error.Message != "boom" {
					t.Fatalf("%s openai = %+v", tc.class, parsed.Error)
				}
				if codePtr(parsed.Error.Code) != stringOrNil(tc.openAICode) {
					t.Fatalf("%s openai code = %v, want %v", tc.class, parsed.Error.Code, tc.openAICode)
				}
				paramWant := ""
				if tc.openAIParam != nil {
					paramWant = tc.openAIParam.(string)
				}
				if paramPtr(parsed.Error.Param) != paramWant {
					t.Fatalf("%s openai param = %v, want %v", tc.class, parsed.Error.Param, tc.openAIParam)
				}
			},
			FormatResponses: nil, // 与 Chat 共用信封,由 FormatOpenAI 用例覆盖
			FormatClaude: func(t *testing.T, status int, body []byte) {
				if status != tc.anthropicStat {
					t.Fatalf("%s anthropic status = %d, want %d", tc.class, status, tc.anthropicStat)
				}
				var parsed struct {
					Type  string `json:"type"`
					Error struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(body, &parsed); err != nil {
					t.Fatalf("%s anthropic body: %v", tc.class, err)
				}
				if parsed.Type != "error" || parsed.Error.Type != tc.anthropicType || parsed.Error.Message != "boom" {
					t.Fatalf("%s anthropic = %+v", tc.class, parsed)
				}
			},
			FormatGemini: func(t *testing.T, status int, body []byte) {
				var parsed struct {
					Error struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
						Status  string `json:"status"`
					} `json:"error"`
				}
				if err := json.Unmarshal(body, &parsed); err != nil {
					t.Fatalf("%s gemini body: %v", tc.class, err)
				}
				if parsed.Error.Status != tc.geminiStatus || parsed.Error.Message != "boom" {
					t.Fatalf("%s gemini = %+v", tc.class, parsed)
				}
				if parsed.Error.Code == 0 {
					t.Fatalf("%s gemini code missing", tc.class)
				}
			},
		} {
			if check == nil {
				continue
			}
			gotStatus, gotBody := ProtocolErrorBody(format, &MaheshvaraError{Class: tc.class, Message: "boom"})
			check(t, gotStatus, gotBody)
		}
	}
}

// 上游真实状态码优先于分类建议值(跨协议翻译保真);细分 code 透传。
func TestProtocolErrorBodyUpstreamOverride(t *testing.T) {
	err := &MaheshvaraError{Class: ErrorClassRateLimit, Code: "slow_down", Status: 429, Message: "quota"}
	status, body := ProtocolErrorBody(FormatOpenAI, err)
	if status != 429 {
		t.Fatalf("status = %d, want 429", status)
	}
	var parsed struct {
		Error struct {
			Code any `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil || parsed.Error.Code != "slow_down" {
		t.Fatalf("code passthrough failed: %s", body)
	}
}

// 未分类错误的兜底是 server/500,不允许空分类外泄。
func TestErrorClassOrDefault(t *testing.T) {
	if got := ErrorClass("").OrDefault(); got != ErrorClassServer {
		t.Fatalf("default class = %q", got)
	}
	status, _ := ProtocolErrorBody(FormatOpenAI, &MaheshvaraError{Message: "x"})
	if status != 500 {
		t.Fatalf("unclassified status = %d, want 500", status)
	}
}

func stringOrNil(s string) string {
	if s == "" {
		return "<nil>"
	}
	return s
}

func codePtr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func paramPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
