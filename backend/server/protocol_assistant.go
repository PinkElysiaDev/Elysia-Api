package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// 协议设计器的 AI harness：生成 → 校验 → 自动修复 → 离线验证，全部在服务端
// 闭环完成。系统提示词由字段目录程序化生成（与 UI 下拉、后端校验同源）；
// 草稿经声明式校验 + 编译 + 注册级校验，失败自动携带 issues 回炉重造；有效
// 草稿用样例请求渲染、用示例响应跑映射，全程不发起真实上游请求。凭证不出
// 服务端。

const (
	customProtocolAssistMaxDocs       = 20
	customProtocolAssistMaxTextBytes  = 512 << 10 // 单个文本输入上限
	customProtocolAssistMaxFileBytes  = 8 << 20   // 单个文件（解码后）上限
	customProtocolAssistMaxTotalBytes = 32 << 20  // 全部输入合计上限
	customProtocolAssistTimeoutSec    = 300       // 助手单轮调用硬性总超时
	customProtocolAssistMaxTokens     = 4096      // 单轮回复输出上限
	customProtocolAssistDefaultRepair = 2         // 默认修复轮次
	customProtocolAssistMaxRepair     = 3         // 修复轮次上限
)

type customProtocolAssistDocument struct {
	Name    string `json:"name"`
	Mime    string `json:"mime,omitempty"`
	Text    string `json:"text,omitempty"`    // 纯文本输入（粘贴内容/文本文件）
	DataURL string `json:"dataUrl,omitempty"` // data: URL（图片与 PDF 等二进制文档）
}

type customProtocolAssistPayload struct {
	SourceID        string                         `json:"sourceId"`
	Model           string                         `json:"model"`
	ProtocolType    string                         `json:"protocolType,omitempty"`
	Message         string                         `json:"message,omitempty"`
	Documents       []customProtocolAssistDocument `json:"documents,omitempty"`
	CurrentConfig   json.RawMessage                `json:"currentConfig,omitempty"`
	ExampleResponse json.RawMessage                `json:"exampleResponse,omitempty"`
	MaxRepairRounds int                            `json:"maxRepairRounds,omitempty"`
}

func (s *Server) adminAssistCustomProtocol(c *gin.Context) {
	var payload customProtocolAssistPayload
	if err := bindAdminJSON(c, &payload); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	store, okStore := s.requireStore(c)
	if !okStore {
		return
	}
	if strings.TrimSpace(payload.SourceID) == "" || strings.TrimSpace(payload.Model) == "" {
		respondFail(c, http.StatusBadRequest, "missing_target", "必须指定助手使用的模型源与模型")
		return
	}
	model, found := findCustomProtocolTestModel(c.Request.Context(), store, payload.SourceID, payload.Model)
	if !found {
		respondFail(c, http.StatusNotFound, "model_not_found",
			fmt.Sprintf("模型源 %q 下没有找到模型 %q", payload.SourceID, payload.Model))
		return
	}

	initialMessages, err := buildAssistConversation(model, payload)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}

	timeout := s.probeTimeout(customProtocolAssistTimeoutSec * time.Second)
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	maxRepair := payload.MaxRepairRounds
	if maxRepair <= 0 {
		maxRepair = customProtocolAssistDefaultRepair
	}
	if maxRepair > customProtocolAssistMaxRepair {
		maxRepair = customProtocolAssistMaxRepair
	}

	started := time.Now()
	system := assistSystemPrompt()
	messages := initialMessages
	var reply string
	var usage *relay.MaheshvaraUsage
	var draft json.RawMessage
	var issues string
	valid := false
	rounds := 0
	for round := 0; round <= maxRepair; round++ {
		rounds = round + 1
		reply, usage, err = s.callAssistModel(ctx, model, system, messages)
		if err != nil {
			respondFail(c, http.StatusBadGateway, "llm_call_failed", err.Error())
			return
		}
		draft, issues, valid = evaluateAssistDraft(reply)
		if valid {
			break
		}
		if round == maxRepair {
			break
		}
		// 修复轮：把校验问题连同上轮输出回传给模型重造。
		messages = append(messages,
			relay.MaheshvaraMessage{Role: "assistant", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: reply}}},
			repairAssistMessage(issues),
		)
	}

	response := gin.H{
		"reply":      reply,
		"model":      model.Name,
		"durationMs": time.Since(started).Milliseconds(),
		"valid":      valid,
		"rounds":     rounds,
	}
	if usage != nil {
		response["usage"] = usage
	}
	if len(draft) > 0 {
		response["config"] = draft
	}
	if issues != "" {
		response["issues"] = issues
	}
	if valid {
		response["verification"] = verifyAssistDraft(draft, payload.ExampleResponse)
	}
	respondOK(c, response)
}

// evaluateAssistDraft 提取并校验草稿：声明式校验 + 编译 + 注册级校验全部内置
// 于 ValidateCustomProtocol。
func evaluateAssistDraft(reply string) (json.RawMessage, string, bool) {
	raw, ok := extractAssistProtocolJSON(reply)
	if !ok {
		return nil, "未能从回复中提取出配置 JSON（需要在 ```json 围栏内输出含 id 和 request 的单个对象）", false
	}
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(raw, &protocol); err != nil {
		return raw, fmt.Sprintf("配置无法解析: %v", err), false
	}
	if err := relay.ValidateCustomProtocol(protocol); err != nil {
		return raw, err.Error(), false
	}
	return raw, "", true
}

func repairAssistMessage(issues string) relay.MaheshvaraMessage {
	text := fmt.Sprintf(
		"上一版配置校验失败，问题如下：\n%s\n\n请修复所有问题后，重新输出完整配置（保持输出契约：说明 + ```json 围栏内的单个完整对象）。",
		issues,
	)
	return relay.MaheshvaraMessage{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: text}}}
}

// verifyAssistDraft 离线验证有效草稿：样例请求渲染 + 示例响应映射，均不发起
// 真实上游请求。
func verifyAssistDraft(draft json.RawMessage, exampleResponse json.RawMessage) gin.H {
	verification := gin.H{}
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(draft, &protocol); err != nil {
		verification["requestError"] = err.Error()
		return verification
	}
	if preview, err := previewCustomProtocolRequest(protocol, defaultCustomProtocolSampleRequest()); err != nil {
		verification["requestError"] = err.Error()
	} else {
		verification["request"] = preview
	}
	if len(exampleResponse) > 0 {
		if mapped, err := relay.CustomProtocolResponseToMaheshvara(exampleResponse, protocol); err != nil {
			verification["mappingError"] = err.Error()
		} else if encoded, err := json.MarshalIndent(mapped, "", "  "); err == nil {
			verification["mappedResponse"] = json.RawMessage(encoded)
		}
	}
	return verification
}

// buildAssistConversation 组装首轮多模态消息：任务说明 + 材料（文本/图片/文档
// part）+ 当前草稿 + 输出要求。
func buildAssistConversation(model storage.Model, payload customProtocolAssistPayload) ([]relay.MaheshvaraMessage, error) {
	if len(payload.Documents) == 0 && strings.TrimSpace(payload.Message) == "" {
		return nil, fmt.Errorf("请至少提供一份文档/图片/文本输入，或填写补充说明")
	}
	if len(payload.Documents) > customProtocolAssistMaxDocs {
		return nil, fmt.Errorf("输入附件最多 %d 个", customProtocolAssistMaxDocs)
	}
	var parts []relay.MaheshvaraContentPart
	total := 0
	var intro strings.Builder
	intro.WriteString("请根据以下材料设计自定义协议配置。\n")
	if protocolType := strings.TrimSpace(payload.ProtocolType); protocolType != "" {
		intro.WriteString(fmt.Sprintf("目标协议类型：%s。\n", protocolType))
	}
	if len(payload.Documents) > 0 {
		intro.WriteString(fmt.Sprintf("共 %d 份输入材料，随后依次给出。\n", len(payload.Documents)))
	}
	intro.WriteString("补充说明/迭代要求：\n")
	if message := strings.TrimSpace(payload.Message); message != "" {
		if len(message) > customProtocolAssistMaxTextBytes {
			return nil, fmt.Errorf("补充说明过长（上限 %d 字节）", customProtocolAssistMaxTextBytes)
		}
		intro.WriteString(message)
		total += len(message)
	} else {
		intro.WriteString("（无）")
	}
	parts = append(parts, relay.MaheshvaraContentPart{Type: relay.MaheshvaraContentText, Text: intro.String()})

	for index, doc := range payload.Documents {
		text := strings.TrimSpace(doc.Text)
		dataURL := strings.TrimSpace(doc.DataURL)
		if text == "" && dataURL == "" {
			continue
		}
		switch {
		case text != "":
			if len(text) > customProtocolAssistMaxTextBytes {
				return nil, fmt.Errorf("材料 %d（%s）文本过长（上限 %d 字节）", index+1, doc.Name, customProtocolAssistMaxTextBytes)
			}
			total += len(text)
			label := firstNonEmptyStringServer(doc.Name, fmt.Sprintf("材料 %d", index+1))
			parts = append(parts, relay.MaheshvaraContentPart{Type: relay.MaheshvaraContentText,
				Text: fmt.Sprintf("\n===== 材料 %d：%s =====\n%s\n===== 材料 %d 结束 =====", index+1, label, text, index+1)})
		case dataURL != "":
			mime, base64Data, err := parseAssistDataURL(dataURL)
			if err != nil {
				return nil, fmt.Errorf("材料 %d（%s）: %v", index+1, doc.Name, err)
			}
			if len(base64Data) > customProtocolAssistMaxFileBytes {
				return nil, fmt.Errorf("材料 %d（%s）过大（上限 %d MiB）", index+1, doc.Name, customProtocolAssistMaxFileBytes>>20)
			}
			total += len(base64Data)
			if mime == "" {
				mime = doc.Mime
			}
			label := firstNonEmptyStringServer(doc.Name, fmt.Sprintf("attachment-%d", index+1))
			if strings.HasPrefix(mime, "image/") {
				parts = append(parts, relay.MaheshvaraContentPart{
					Type: relay.MaheshvaraContentImage, ImageBase64: string(base64Data),
					MediaType: mime, FileName: label,
				})
			} else {
				// 文档（PDF 等）：OpenAI 系期望 file_data 为 data: URL，
				// Claude/Gemini 期望裸 base64 —— 按目标平台写入对应形态。
				fileData := string(base64Data)
				switch relay.NormalizeAPIFormat(model.Platform) {
				case relay.APIFormatChatCompletions, relay.APIFormatResponses:
					fileData = dataURL
				}
				parts = append(parts, relay.MaheshvaraContentPart{
					Type: relay.MaheshvaraContentDocument, FileData: fileData,
					MediaType: mime, FileName: label,
				})
			}
		}
		if total > customProtocolAssistMaxTotalBytes {
			return nil, fmt.Errorf("输入材料总量超过 %d MiB 上限", customProtocolAssistMaxTotalBytes>>20)
		}
	}

	if len(payload.CurrentConfig) > 0 {
		parts = append(parts, relay.MaheshvaraContentPart{Type: relay.MaheshvaraContentText,
			Text: fmt.Sprintf("\n===== 当前配置草稿（在此基础上迭代，保持未提及部分不变）=====\n%s\n===== 当前配置结束 =====", string(payload.CurrentConfig))})
	}
	if len(payload.ExampleResponse) > 0 {
		parts = append(parts, relay.MaheshvaraContentPart{Type: relay.MaheshvaraContentText,
			Text: fmt.Sprintf("\n===== 上游示例响应（返回体构造的结构与映射依据；离线验证会用它检验映射）=====\n%s\n===== 示例响应结束 =====", string(payload.ExampleResponse))})
	}
	parts = append(parts, relay.MaheshvaraContentPart{Type: relay.MaheshvaraContentText,
		Text: "请按系统指令输出：设计说明 + ```json 围栏的完整配置。"})

	return []relay.MaheshvaraMessage{{Role: "user", Content: parts}}, nil
}

// assistSystemPrompt 由字段目录程序化生成，与 schema 端点/校验同源。
func assistSystemPrompt() string {
	schema := relay.CustomProtocolSchemaFor()
	var b strings.Builder
	b.WriteString("你是 Elysia API「协议设计器」的配置助手。用户会提供某第三方 API 的文档、示例请求/响应或截图，你的任务是产出一份「自定义协议」JSON 配置（字段级映射模型），让网关把这个 API 接入内部统一协议 Maheshvara。\n\n")

	b.WriteString("## 输出契约（必须严格遵守）\n1. 先用简体中文简述设计要点（映射依据、关键取舍）。\n2. 然后输出一个 ```json 围栏，内含完整的配置对象（单个 JSON 对象；不要数组包裹、不要注释、不要尾逗号）。\n3. 全部回复只包含一份配置；信息不足时在说明中列出缺失项并给出当前最合理的配置。\n\n")

	b.WriteString("## 配置结构\n顶层字段：id（必填，短的小写英文标识）、name、version、type（llm 默认 / reranker / embedding 预留 / x- 前缀扩展）、request（必填）、response。\n\n")

	b.WriteString("### request（网关 → 上游）\n")
	b.WriteString("- method：GET/POST/PUT/PATCH/DELETE，默认 POST。\n")
	b.WriteString("- path：相对模型源 baseUrl 的路径，支持 {{maheshvara.<字段>}} 插值，不能含 scheme。\n")
	b.WriteString("- headers / query：静态键值对（不得含认证头，认证走 auth）。\n")
	b.WriteString("- contentType：默认 application/json。\n")
	b.WriteString("- auth：{\"mode\": \"bearer|none|header|query\", \"header\": \"...\", \"prefix\": \"...\", \"query\": \"...\"}。按文档的认证方式选择：Bearer/token 类用 bearer（默认，可省略）；X-Api-Key 类用 header；key 参数类用 query；无需认证用 none。\n")
	b.WriteString("- body：请求体的字段级构造树。容器为普通 JSON 对象/数组；叶子二选一：\n")
	b.WriteString("  - 字段引用 {\"field\": \"<请求字段目录中的字段>\", \"mode\": \"json|string\", \"default\": <可选 JSON 字面量>, \"omitIfEmpty\": <可选 true>}。mode json 以原生 JSON 值插入（对象/数组/数字/布尔必须用它）；mode string 以字符串插入（纯文本字段）。default 是 Maheshvara 侧字段缺失或为空时的兜底值；omitIfEmpty 为 true 时渲染后为空则删除该键（可选参数建议开启）。\n")
	b.WriteString("  - 常量 {\"value\": <任意 JSON>}：上游必填但 Maheshvara 无对应的字段（版本号、固定格式参数等）用它。\n\n")

	b.WriteString("### response（上游 → Maheshvara，只支持 JSON 响应体）\n")
	b.WriteString("- body：返回体构造树（推荐）。容器为普通 JSON 对象/数组，结构按上游示例响应搭建；叶子为映射标注 {\"field\": \"<响应字段目录中的字段>\", \"value\": <示例值>, \"transform\": \"<可选>\"}（value 填示例值便于理解）或纯占位 {\"value\": ...}。数组层级按示例保留（如 choices 数组只需在第 0 项标注映射）。\n")
	b.WriteString("- fields：等效的行列表形式 [{\"path\", \"field\", \"transform\"?}]，path 支持点路径与数组下标（choices[0].delta.content）。与 body 二选一，不要同时声明。\n")
	b.WriteString("- 尽量映射完整：text/reasoning/tool_calls/usage/stop_reason/id/model/error，文档里有就配。\n")
	b.WriteString("- stream：仅当文档描述 SSE 流式时配置 {\"payloadPath\": \"...\", \"mode\": \"delta|cumulative\", \"events\": [...], \"doneValues\": [\"[DONE]\"], \"response\": {\"body\": {...} 或 \"fields\": [...]}}。\n\n")

	b.WriteString("## 请求字段目录（request.body 叶子 field 可用值）\n")
	for _, field := range schema.RequestFields {
		fmt.Fprintf(&b, "- %s — %s（%s）\n", field.Name, field.Label, field.Shape)
	}
	b.WriteString("\n## 响应字段目录（response 映射的 field 可用值）\n")
	for _, field := range schema.ResponseFields {
		fmt.Fprintf(&b, "- %s — %s\n", field.Name, field.Label)
	}
	b.WriteString("- metadata 可带子键（如 metadata.vendor），用于携带上游特有元数据。\n")
	b.WriteString("\n## transform 可选值（一般无需指定；usage.* 默认已按 int 处理）\n")
	b.WriteString(strings.Join(schema.Transforms, "、") + "\n")

	b.WriteString("\n## 设计要点\n")
	b.WriteString("- 认证方式进 auth，不进 headers；不要把文档中的示例密钥照抄进配置。\n")
	b.WriteString("- usage 整体对象直接用 field \"usage\"（键名自动识别）；只有结构特殊时才逐项映射 usage.input_tokens 等。\n")
	b.WriteString("- 从示例响应/截图推断结构时，数组层级不能丢。\n")
	b.WriteString("- 上游要求的固定参数（版本号、格式等）用常量 value 提供。\n")
	b.WriteString("- 消息/工具与某线制同形时，request.shape（openai-chat/anthropic/gemini/responses）直接复用内置整形，不要手写字段级转换。\n")
	b.WriteString("- 条件包含用叶子的 when（对请求求值的条件）与 omitIf（值等即省略）；流式终止判定用 stream.finishWhen/statusWhen，类型化终止值用 stream.done。\n")
	b.WriteString("- 每类事件形状不同的流用 stream.frames（事件名或 match 谓词选帧）；工具调用分帧到达时用 frame.tool（身份帧给 id/name，参数帧给 argumentsPath，按 idPath 或 indexPath 关联）。\n")
	b.WriteString("- 键名不符合内置别名表时用 aliases 声明（textKeys/usage/toolCall，支持点路径）；数组内按类型分块提取用 textFilter/reasoningFilter。\n")

	// few-shot：内嵌预置协议作为完整范例（与首次启动播种的定义同源）。
	if example, ok := findPresetConfig(presetProtocolAnthropicMessagesID); ok {
		if encoded, err := json.Marshal(example); err == nil {
			b.WriteString("\n## 完整范例（预置协议 " + example.ID + "，集中示范 shape/frames/match/tool/别名/元素过滤）\n```json\n" + string(encoded) + "\n```\n")
		}
	}
	return b.String()
}

// callAssistModel 直连所选模型源，支持多轮对话（修复循环）。
func (s *Server) callAssistModel(ctx context.Context, model storage.Model, system string, messages []relay.MaheshvaraMessage) (string, *relay.MaheshvaraUsage, error) {
	request := &relay.MaheshvaraRequest{
		Model:           model.Name,
		Instructions:    system,
		MaxOutputTokens: customProtocolAssistMaxTokens,
		Messages:        messages,
	}
	format := relay.NormalizeAPIFormat(model.Platform)
	body, err := renderAssistRequestBody(request, format)
	if err != nil {
		return "", nil, fmt.Errorf("构建助手请求失败: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, probeEndpoint(model), bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	httpRequest.Header.Set("Content-Type", contentTypeJSON)
	applyProbeAuth(httpRequest, model)
	client := &http.Client{Transport: relay.NewSecureTransport()}
	response, err := client.Do(httpRequest)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, customProtocolTestBodyLimit))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", nil, fmt.Errorf("上游模型返回 %d: %s", response.StatusCode, truncateForDisplay(string(raw), 2048))
	}

	var maheshvaraResponse *relay.MaheshvaraResponse
	switch format {
	case relay.APIFormatAnthropic:
		var decoded relay.ClaudeResponse
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", nil, fmt.Errorf("解析上游响应失败: %w", err)
		}
		if maheshvaraResponse, err = relay.AnthropicResponseToMaheshvara(&decoded); err != nil {
			return "", nil, err
		}
	case relay.APIFormatGemini:
		var decoded relay.GeminiResponse
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", nil, fmt.Errorf("解析上游响应失败: %w", err)
		}
		if maheshvaraResponse, err = relay.GeminiResponseToMaheshvara(&decoded); err != nil {
			return "", nil, err
		}
	case relay.APIFormatResponses:
		var decoded relay.OpenAIResponsesResponse
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", nil, fmt.Errorf("解析上游响应失败: %w", err)
		}
		if maheshvaraResponse, err = relay.OpenAIResponsesResponseToMaheshvara(&decoded); err != nil {
			return "", nil, err
		}
	default:
		var decoded relay.OpenAIResponse
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", nil, fmt.Errorf("解析上游响应失败: %w", err)
		}
		if maheshvaraResponse, err = relay.OpenAIChatResponseToMaheshvara(&decoded); err != nil {
			return "", nil, err
		}
	}

	var text strings.Builder
	for _, item := range maheshvaraResponse.Output {
		for _, part := range item.Content {
			if part.Type == relay.MaheshvaraContentText && part.Text != "" {
				text.WriteString(part.Text)
				if !strings.HasSuffix(part.Text, "\n") {
					text.WriteString("\n")
				}
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", maheshvaraResponse.Usage, fmt.Errorf("模型未返回文本（可能不支持所给的多模态输入，请换用支持文档/视觉的模型，或改用文本粘贴）: %s", truncateForDisplay(string(raw), 1024))
	}
	return text.String(), maheshvaraResponse.Usage, nil
}

// renderAssistRequestBody 按模型源平台把 Maheshvara 请求渲染为线格式。
func renderAssistRequestBody(request *relay.MaheshvaraRequest, format string) ([]byte, error) {
	switch format {
	case relay.APIFormatAnthropic:
		return relay.MaheshvaraToAnthropic(request)
	case relay.APIFormatGemini:
		return relay.MaheshvaraToGemini(request)
	case relay.APIFormatResponses:
		return relay.MaheshvaraToOpenAIResponses(request, nil)
	default:
		return relay.MaheshvaraToOpenAIChat(request)
	}
}

func parseAssistDataURL(dataURL string) (mime string, data []byte, err error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", nil, fmt.Errorf("dataUrl 必须以 data: 开头")
	}
	comma := strings.Index(dataURL, ",")
	if comma < 0 {
		return "", nil, fmt.Errorf("dataUrl 缺少逗号分隔符")
	}
	header := dataURL[5:comma]
	payload := dataURL[comma+1:]
	if !strings.HasSuffix(header, ";base64") {
		return "", nil, fmt.Errorf("dataUrl 仅支持 base64 编码")
	}
	mime = strings.TrimSuffix(header, ";base64")
	data, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, fmt.Errorf("base64 解码失败: %w", err)
	}
	return mime, data, nil
}

// extractAssistProtocolJSON 从回复中提取配置 JSON：优先 ```json 围栏（取最后
// 一个能解析出 id/request 的），回退到首个完整 JSON 对象。
func extractAssistProtocolJSON(reply string) (json.RawMessage, bool) {
	blocks := extractFencedBlocks(reply)
	for i := len(blocks) - 1; i >= 0; i-- {
		if raw, ok := decodeProtocolCandidate(blocks[i]); ok {
			return raw, true
		}
	}
	for offset := 0; offset < len(reply); offset++ {
		if reply[offset] != '{' {
			continue
		}
		if raw, ok := decodeProtocolCandidate(reply[offset:]); ok {
			return raw, true
		}
	}
	return nil, false
}

func extractFencedBlocks(reply string) []string {
	var blocks []string
	for offset := 0; offset < len(reply); {
		start := strings.Index(reply[offset:], "```")
		if start < 0 {
			break
		}
		start += offset
		lineEnd := strings.IndexByte(reply[start:], '\n')
		if lineEnd < 0 {
			break
		}
		bodyStart := start + lineEnd + 1
		end := strings.Index(reply[bodyStart:], "```")
		if end < 0 {
			break
		}
		blocks = append(blocks, reply[bodyStart:bodyStart+end])
		offset = bodyStart + end + 3
	}
	return blocks
}

// decodeProtocolCandidate 尝试从文本起始解码一个完整 JSON 对象，并确认它
// 看起来像协议配置（含 id 与 request 键）。
func decodeProtocolCandidate(text string) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	if _, ok := value["id"]; !ok {
		return nil, false
	}
	if _, ok := value["request"]; !ok {
		return nil, false
	}
	return json.RawMessage(trimmed[:decoder.InputOffset()]), true
}

func firstNonEmptyStringServer(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
