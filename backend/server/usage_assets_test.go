package server

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newExternalizeRecord 构造一个启用外置的记录（1MiB 上限，与线上默认一致）。
func newExternalizeRecord(requestID string) *usageRecord {
	return &usageRecord{
		RequestID: requestID,
		StartedAt: time.Now(),
		bodyOpts:  usageBodyOptions{initialized: true, maxBytes: 1024 * 1024, externalize: true},
		assets:    newAssetSink(requestID),
	}
}

// base64Len 生成指定长度的合法 base64 字符串（'A' 是合法字符）。
func base64Len(n int) string { return strings.Repeat("A", n) }

func TestAssetSinkExtractsOpenAIDataURI(t *testing.T) {
	record := newExternalizeRecord("req_openai")
	payload := base64Len(600)
	body := []byte(`{"model":"gpt","messages":[{"role":"user","content":[
		{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,` + payload + `"}}]}]}`)

	got := record.sanitizeBody(body)
	if got.Truncated {
		t.Fatal("small body must not be truncated")
	}
	if strings.Contains(got.Content, payload) {
		t.Fatal("base64 payload must be removed from body")
	}
	if !strings.Contains(got.Content, AssetPlaceholderPrefix+"req_openai/") {
		t.Fatalf("placeholder missing in body: %s", got.Content)
	}
	if record.assets.count() != 1 {
		t.Fatalf("expected 1 registered asset, got %d", record.assets.count())
	}
	item := record.assets.items[0]
	if item.Ext != "png" || item.Mime != "image/png" {
		t.Fatalf("asset meta mismatch: %+v", item)
	}
	if len(item.Data) == 0 {
		t.Fatal("decoded data must be registered")
	}
	// JSON 结构必须保持合法（占位符是普通字符串值）。
	if !json.Valid([]byte(got.Content)) {
		t.Fatalf("body must remain valid JSON, got: %s", got.Content)
	}
}

func TestAssetSinkExtractsClaudeSourceData(t *testing.T) {
	record := newExternalizeRecord("req_claude")
	payload := base64Len(600)
	body := []byte(`{"model":"claude","messages":[{"role":"user","content":[
		{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"` + payload + `"}}]}]}`)
	got := record.sanitizeBody(body)
	if strings.Contains(got.Content, payload) {
		t.Fatal("claude source.data must be externalized")
	}
	if record.assets.count() != 1 || record.assets.items[0].Ext != "jpg" {
		t.Fatalf("claude asset meta mismatch: %+v", record.assets.items)
	}
	_ = got
}

func TestAssetSinkExtractsGeminiInlineData(t *testing.T) {
	record := newExternalizeRecord("req_gemini")
	payload := base64Len(600)
	body := []byte(`{"contents":[{"parts":[{"text":"hi"},{"inline_data":{"mime_type":"audio/wav","data":"` + payload + `"}}]}]}`)
	record.sanitizeBody(body)
	// inline_data/mime_type 经 normalizedKey 归一为 inlineData/mimeType 识别。
	if record.assets.count() != 1 {
		t.Fatalf("gemini inlineData must be externalized, items=%+v", record.assets.items)
	}
	if record.assets.items[0].Ext != "wav" {
		t.Fatalf("wav ext expected, got %q", record.assets.items[0].Ext)
	}
}

func TestAssetSinkExtractsB64JSON(t *testing.T) {
	record := newExternalizeRecord("req_img")
	payload := base64Len(700)
	body := []byte(`{"data":[{"b64_json":"` + payload + `"}]}`)
	record.sanitizeBody(body)
	if record.assets.count() != 1 || record.assets.items[0].Ext != "png" {
		t.Fatalf("b64_json asset expected, items=%+v", record.assets.items)
	}
}

func TestAssetSinkSkipsShortAndInvalidBase64(t *testing.T) {
	record := newExternalizeRecord("req_skip")
	short := base64Len(120)                                        // 低于 512 阈值
	invalid := "data:image/png;base64," + strings.Repeat("!", 600) // 非 base64 字符
	body := []byte(`{"a":"` + short + `","b":"` + invalid + `","c":"data:image/png;base64,` + base64Len(100) + `"}`)
	got := record.sanitizeBody(body)
	if record.assets.count() != 0 {
		t.Fatalf("nothing should be externalized, got %d", record.assets.count())
	}
	if !strings.Contains(got.Content, short) || !strings.Contains(got.Content, "!!!!") {
		t.Fatal("short/invalid values must be kept verbatim")
	}
}

func TestAssetSinkDedupesIdenticalContent(t *testing.T) {
	record := newExternalizeRecord("req_dedupe")
	payload := base64Len(600)
	// 同一图片出现在 incoming 与 outgoing（两段清洗共用一个 sink）。
	body1 := []byte(`{"url":"data:image/png;base64,` + payload + `"}`)
	body2 := []byte(`{"source":{"type":"base64","media_type":"image/png","data":"` + payload + `"}}`)
	first := record.sanitizeBody(body1)
	second := record.sanitizeBody(body2)
	if record.assets.count() != 1 {
		t.Fatalf("identical content must dedupe to 1 asset, got %d", record.assets.count())
	}
	if !strings.Contains(first.Content, strings.TrimSuffix(second.Content, `}`)[len(`{"url":"`):]) &&
		strings.Count(first.Content+second.Content, AssetPlaceholderPrefix) != 2 {
		t.Fatalf("both bodies must reference the placeholder: %s | %s", first.Content, second.Content)
	}
}

func TestSanitizeBodyTruncatesAfterExtraction(t *testing.T) {
	record := newExternalizeRecord("req_trunc")
	record.bodyOpts = usageBodyOptions{initialized: true, maxBytes: 200, externalize: true}
	payload := base64Len(600)
	// 巨量文本 + 图片：先外置再截断，图片不能被拦腰截断丢失。
	body := []byte(`{"url":"data:image/png;base64,` + payload + `","pad":"` + strings.Repeat("x", 4096) + `"}`)
	got := record.sanitizeBody(body)
	if record.assets.count() != 1 {
		t.Fatalf("image must be extracted before truncation, got %d assets", record.assets.count())
	}
	if len(got.Content) > 200+64 { // 截断 + 占位符替换后的合理余量
		t.Fatalf("content must be truncated near limit, len=%d", len(got.Content))
	}
}

func TestSanitizeBodyZeroMaxBytesDropsContent(t *testing.T) {
	record := newExternalizeRecord("req_zero")
	record.bodyOpts = usageBodyOptions{initialized: true, maxBytes: 0, externalize: true}
	got := record.sanitizeBody([]byte(`{"a":1}`))
	if got.Content != "" || got.Truncated {
		t.Fatalf("maxBytes=0 must drop body entirely, got %+v", got)
	}
}

func TestSanitizeBodyKeepsLegacyDefaultForBareRecord(t *testing.T) {
	// 直接构造的记录（未走 initUsageRecord）bodyOpts 为零值：
	// 上限按历史 1MiB 默认、不外置。
	record := &usageRecord{RequestID: "bare", StartedAt: time.Now()}
	payload := base64Len(600)
	body := []byte(`{"url":"data:image/png;base64,` + payload + `"}`)
	got := record.sanitizeBody(body)
	if !strings.Contains(got.Content, payload) {
		t.Fatal("bare record must not externalize (legacy behavior)")
	}
	if got.Truncated {
		t.Fatal("small body must not truncate")
	}
}

func TestAssetSinkExtractFromSSE(t *testing.T) {
	record := newExternalizeRecord("req_sse")
	payload := base64Len(600)
	sse := "event: chunk\n" +
		`data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"image":{"b64_json":"` + payload + `"}}` + "\n\n" +
		"data: [DONE]\n"
	out := record.assets.extractFromSSE(sse)
	if strings.Contains(out, payload) {
		t.Fatal("SSE b64_json must be externalized")
	}
	if !strings.Contains(out, `"content":"hi"`) || !strings.Contains(out, "[DONE]") {
		t.Fatalf("non-media SSE lines must be preserved: %s", out)
	}
	if !strings.HasPrefix(strings.SplitN(out, "\n", 2)[1], "data: ") {
		t.Fatalf("data: prefix must be preserved: %s", out)
	}
	if record.assets.count() != 1 {
		t.Fatalf("1 asset expected, got %d", record.assets.count())
	}
}

// 回归：CRLF/尾空白是合法 SSE 行尾。旧实现用 TrimSuffix 重建行，payload
// 被 TrimSpace 后不以原文结尾导致不匹配——base64 残留且行尾拼接出两份 JSON。
func TestAssetSinkExtractFromSSEWithCRLF(t *testing.T) {
	record := newExternalizeRecord("req_sse_crlf")
	payload := base64Len(600)
	sse := "event: chunk\r\n" +
		`data: {"image":{"b64_json":"` + payload + `"}}` + "\r\n" +
		"data: [DONE]\r\n"
	out := record.assets.extractFromSSE(sse)
	if strings.Contains(out, payload) {
		t.Fatal("CRLF SSE b64_json must still be externalized")
	}
	if record.assets.count() != 1 {
		t.Fatalf("1 asset expected, got %d", record.assets.count())
	}
	// 媒体行被替换为占位符 JSON，且不能出现行内双 JSON 拼接。
	mediaLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, AssetPlaceholderPrefix) {
			mediaLine = strings.TrimSpace(line)
		}
	}
	if mediaLine == "" {
		t.Fatalf("placeholder line missing: %q", out)
	}
	if !json.Valid([]byte(strings.TrimPrefix(mediaLine, "data: "))) {
		t.Fatalf("replaced media line must be a single valid JSON value: %q", mediaLine)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("DONE line must survive: %q", out)
	}
}

func TestWriteUsageAssetsAndSkipExisting(t *testing.T) {
	root := t.TempDir()
	sink := newAssetSink("req_write")
	data, _ := base64.StdEncoding.DecodeString(base64Len(600))
	if _, ok := sink.register("image/png", "", base64Len(600)); !ok {
		t.Fatal("register failed")
	}
	n, err := writeUsageAssets(root, sink.items)
	if err != nil || n != 1 {
		t.Fatalf("writeUsageAssets = %d, %v", n, err)
	}
	item := sink.items[0]
	path := filepath.Join(root, item.Hash+"."+item.Ext)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("asset file must exist: %v", err)
	}
	// 第二次写（同哈希，无论哪个请求）跳过——扁平内容寻址全局去重。
	if n, err := writeUsageAssets(root, sink.items); err != nil || n != 0 {
		t.Fatalf("re-write should skip existing: %d, %v", n, err)
	}
	_ = data
}

func TestParseAssetFileName(t *testing.T) {
	if _, _, ok := parseAssetFileName("0123456789abcdef.png"); !ok {
		t.Fatal("valid name rejected")
	}
	for _, bad := range []string{"../../etc/passwd", "short.png", "0123456789abcdef", "0123456789abcdeg.png", "0123456789abcdef.PNG"} {
		if _, _, ok := parseAssetFileName(bad); ok {
			t.Fatalf("invalid name accepted: %s", bad)
		}
	}
}

func TestMediaExtFromMime(t *testing.T) {
	cases := map[string]string{
		"image/png":       "png",
		"image/jpeg":      "jpg",
		"audio/mpeg":      "mp3",
		"audio/wav":       "wav",
		"video/mp4":       "mp4",
		"application/pdf": "pdf",
		"text/plain":      "bin",
	}
	for mime, ext := range cases {
		if got := mediaExtFromMime(mime); got != ext {
			t.Fatalf("mediaExtFromMime(%q) = %q, want %q", mime, got, ext)
		}
	}
}

func TestAssetSinkCloneIsDeep(t *testing.T) {
	sink := newAssetSink("req_clone")
	if _, ok := sink.register("image/png", "", base64Len(600)); !ok {
		t.Fatal("register failed")
	}
	cloned := sink.clone()
	// 修改克隆体的数据不影响原 sink（断开底层数组别名）。
	cloned.items[0].Data[0] ^= 0xFF
	if sink.items[0].Data[0] == 0xFF {
		t.Fatal("clone must deep-copy asset data")
	}
	if cloned.count() != 1 {
		t.Fatal("clone must carry assets")
	}
}
