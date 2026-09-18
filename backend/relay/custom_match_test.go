package relay

import (
	"strings"
	"testing"
)

func matchPayload(t *testing.T, body string) any {
	t.Helper()
	raw, ok := customMatchValue([]byte(body))
	if !ok {
		t.Fatalf("bad json: %s", body)
	}
	return raw
}

func TestCustomMatchEvalOps(t *testing.T) {
	root := matchPayload(t, `{
		"finish": false, "count": 0, "name": "toolu_1", "state": "completed",
		"tags": ["stop", "length"], "reason": null, "budget": 4096, "text": "hello world"
	}`)
	cases := []struct {
		op    string
		value string
		want  bool
	}{
		{MatchOpNonEmpty, "", false},      // finish=false 视为空
		{MatchOpNonEmpty, "", false},      // count=0 亦视为空
		{MatchOpIsEmpty, "", true},        // false 是空
		{MatchOpEquals, `false`, true},    // 类型化布尔相等
		{MatchOpEquals, `"false"`, false}, // 字符串 "false" 不等于布尔
		{MatchOpEquals, `4096`, true},     // 数字相等
		{MatchOpEquals, `4096.0`, true},   // 数值比较
		{MatchOpNotEquals, `"done"`, true},    // state=completed ≠ done
		{MatchOpIn, `["stop","length"]`, false},
		{MatchOpIsNull, "", true},          // reason 为 null
		{MatchOpNotNull, "", false},        // reason 为 null → notNull 假
		{MatchOpIsTrue, "", false},         // finish 是布尔 false
		{MatchOpIsFalse, "", true},         // finish 是布尔 false
		{MatchOpContains, `"hello"`, true}, // 字符串包含
		{MatchOpGt, `4095`, true},          // budget > 4095
		{MatchOpLte, `4096`, true},
	}
	paths := []string{"finish", "count", "finish", "finish", "finish", "budget", "budget", "state", "state", "reason", "reason", "finish", "finish", "text", "budget", "budget"}
	if len(paths) != len(cases) {
		t.Fatalf("test table misaligned: %d cases vs %d paths", len(cases), len(paths))
	}
	for index, tc := range cases {
		match := CustomProtocolMatch{Path: paths[index], Op: tc.op}
		if tc.value != "" {
			match.Value = []byte(tc.value)
		}
		if got := customMatchEval(root, match); got != tc.want {
			t.Errorf("op=%s path=%s value=%s: got %v want %v", tc.op, paths[index], tc.value, got, tc.want)
		}
	}
	// in 判定换成 tags 路径(数组内查找)。
	if !customMatchEval(root, CustomProtocolMatch{Path: "state", Op: MatchOpIn, Value: []byte(`["completed","done"]`)}) {
		t.Error("state in [completed,done] must hold")
	}
	// 缺失路径:isNull/isFalse 成立,其余不成立。
	if !customMatchEval(root, CustomProtocolMatch{Path: "missing", Op: MatchOpIsNull}) {
		t.Error("missing path isNull must hold")
	}
	if !customMatchEval(root, CustomProtocolMatch{Path: "missing", Op: MatchOpIsFalse}) {
		t.Error("missing path isFalse must hold (absent counts as false)")
	}
	if customMatchEval(root, CustomProtocolMatch{Path: "missing", Op: MatchOpNonEmpty}) {
		t.Error("missing path nonEmpty must not hold")
	}
	// 对象值 equals:规范化 JSON 文本比较(键序无关)。
	objRoot := matchPayload(t, `{"output":{"status":{"phase":"done"}}}`)
	if !customMatchEval(objRoot, CustomProtocolMatch{Path: "output.status", Op: MatchOpEquals, Value: []byte(`{"phase":"done"}`)}) {
		t.Error("object equality must be order-insensitive")
	}
}

func TestValidateCustomProtocolMatch(t *testing.T) {
	if err := validateCustomProtocolMatch("m", CustomProtocolMatch{Path: "finish", Op: MatchOpIsTrue}); err != nil {
		t.Fatalf("isTrue needs no value: %v", err)
	}
	if err := validateCustomProtocolMatch("m", CustomProtocolMatch{Path: "finish"}); err != nil {
		t.Fatalf("default op: %v", err)
	}
	if err := validateCustomProtocolMatch("m", CustomProtocolMatch{Op: MatchOpEquals, Value: []byte(`1`)}); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("missing path must fail, got %v", err)
	}
	if err := validateCustomProtocolMatch("m", CustomProtocolMatch{Path: "a", Op: "bogus"}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("bad op must fail, got %v", err)
	}
	if err := validateCustomProtocolMatch("m", CustomProtocolMatch{Path: "a", Op: MatchOpEquals}); err == nil || !strings.Contains(err.Error(), "requires value") {
		t.Fatalf("equals without value must fail, got %v", err)
	}
	if err := validateCustomProtocolMatch("m", CustomProtocolMatch{Path: "a", Op: MatchOpIn, Value: []byte(`"stop"`)}); err == nil || !strings.Contains(err.Error(), "array") {
		t.Fatalf("in with non-array must fail, got %v", err)
	}
	if err := validateCustomProtocolMatch("m", CustomProtocolMatch{Path: "bad[path"}); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("bad path must fail, got %v", err)
	}
}
