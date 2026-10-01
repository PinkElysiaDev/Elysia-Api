package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/relay"
)

// protocolDraftDetail 是 `elysia help protocol draft` 的详细说明：接入工作
// 流 + 配置结构目录 + 完整范例，按需拉取（不常驻系统提示词）。
func protocolDraftDetail() string {
	var b strings.Builder
	b.WriteString("接入工作流：\n")
	b.WriteString("1. 阅读用户给的 API 文档/示例，识别：认证方式、端点路径、请求/响应结构、是否 SSE 流式、模型列表端点（若有）。\n")
	b.WriteString("2. elysia protocol draft '<完整配置 JSON>' 提交草稿（有响应示例时带 --example '<示例 JSON>' 做离线映射校验）；校验失败会返回 issues，修复后重新提交。\n")
	b.WriteString("3. elysia protocol preview 离线自查请求形状，再向用户询问测试目标（baseUrl / API key），elysia protocol test --stream 与 elysia protocol models 真实测试（受权限策略控制；凭证用 --base-url / --api-key 传入），按结果修正。\n")
	b.WriteString("4. 通过后 elysia protocol save 保存；接入调用还需配套模型源（--platform custom:<协议id>）与模型组。\n\n")
	writeProtocolReference(&b, true)
	return b.String()
}

// writeProtocolReference 输出协议配置结构参考与范例（字段目录与校验/下拉
// 同源生成）。
func writeProtocolReference(b *strings.Builder, includeExample bool) {
	b.WriteString("顶层字段：id（必填，短的小写英文标识）、name、version、type（llm 默认 / reranker / embedding 预留 / x- 前缀扩展）、request（必填）、response。\n\n")

	b.WriteString("### request（网关 → 上游）\n")
	b.WriteString("- method：GET/POST/PUT/PATCH/DELETE，默认 POST。\n")
	b.WriteString("- path：相对模型源 baseUrl 的路径，支持 {{maheshvara.<字段>}} 插值，不能含 scheme。\n")
	b.WriteString("- pathStream：流式请求的路径覆盖（如 Gemini 的 :streamGenerateContent?alt=sse）。\n")
	b.WriteString("- headers / query：静态键值对（不得含认证头，认证走 auth）。\n")
	b.WriteString("- contentType：默认 application/json。\n")
	b.WriteString("- auth：{\"mode\": \"bearer|none|header|query\", \"header\": \"...\", \"prefix\": \"...\", \"query\": \"...\"}。Bearer/token 类用 bearer（默认）；X-Api-Key 类用 header；key 参数类用 query；无需认证用 none。\n")
	b.WriteString("- body：请求体的字段级构造树。容器为普通 JSON 对象/数组；叶子二选一：\n")
	b.WriteString("  - 字段引用 {\"field\": \"<请求字段目录中的字段>\", \"mode\": \"json|string\", \"default\": <可选 JSON 字面量>, \"omitIfEmpty\": <可选 true>}。mode json 以原生 JSON 值插入（对象/数组/数字/布尔必须用它）；mode string 以字符串插入。\n")
	b.WriteString("  - 常量 {\"value\": <任意 JSON>}：上游必填但 Maheshvara 无对应的字段（版本号、固定格式参数等）用它。\n\n")

	b.WriteString("### response（上游 → Maheshvara，只支持 JSON 响应体）\n")
	b.WriteString("- body：返回体构造树（推荐）。容器为普通 JSON 对象/数组，结构按上游示例响应搭建；叶子为映射标注 {\"field\": \"<响应字段目录中的字段>\", \"value\": <示例值>, \"transform\": \"<可选>\"} 或纯占位 {\"value\": ...}。数组层级按示例保留（如 choices 数组只需在第 0 项标注映射）。\n")
	b.WriteString("- fields：等效行列表 [{\"path\", \"field\", \"transform\"?}]，path 支持点路径与数组下标（choices[0].delta.content）。与 body 二选一。\n")
	b.WriteString("- 尽量映射完整：text/reasoning/tool_calls/usage/stop_reason/id/model/error，文档里有就配。\n")
	b.WriteString("- stream：仅当文档描述 SSE 流式时配置 {\"payloadPath\": \"...\", \"mode\": \"delta|cumulative\", \"events\": [...], \"doneValues\": [\"[DONE]\"], \"response\": {\"body\": {...} 或 \"fields\": [...]}}。\n\n")

	schema := relay.CustomProtocolSchemaFor()
	b.WriteString("请求字段目录（request.body 叶子 field 可用值）：\n")
	for _, field := range schema.RequestFields {
		fmt.Fprintf(b, "- %s — %s（%s）\n", field.Name, field.Label, field.Shape)
	}
	b.WriteString("\n响应字段目录（response 映射的 field 可用值）：\n")
	for _, field := range schema.ResponseFields {
		fmt.Fprintf(b, "- %s — %s\n", field.Name, field.Label)
	}
	b.WriteString("- metadata 可带子键（如 metadata.vendor），用于携带上游特有元数据。\n")
	b.WriteString("\ntransform 可选值（一般无需指定；usage.* 默认已按 int 处理）：\n")
	b.WriteString(strings.Join(schema.Transforms, "、") + "\n")

	b.WriteString("\n设计要点：\n")
	b.WriteString("- usage 整体对象直接用 field \"usage\"（键名自动识别）；只有结构特殊时才逐项映射。\n")
	b.WriteString("- 从示例响应/截图推断结构时，数组层级不能丢。\n")
	b.WriteString("- 上游要求的固定参数（版本号、格式等）用常量 value 提供。\n")
	b.WriteString("- 消息/工具与某线制同形时优先用 request.shape（openai-chat/anthropic/gemini/responses）复用内置整形。\n")
	b.WriteString("- 条件包含用叶子的 when 与 omitIf；流式终止判定用 stream.finishWhen/statusWhen，类型化终止值用 stream.done。\n")
	b.WriteString("- 每类事件形状不同的流用 stream.frames（事件名或 match 谓词选帧）；工具调用分帧到达时用 frame.tool。\n")
	b.WriteString("- 键名不符合内置别名表时用 aliases 声明；数组内按类型分块提取用 textFilter/reasoningFilter。\n")
	b.WriteString("- 需要参考成熟写法时可先 `elysia protocol read --id <id>` 读取内置预置协议（chat-completions-api / responses-api / anthropic-api / gemini-api）。\n")
	b.WriteString("- `response.adapter` 与 `response.stream.adapter` 可分别选择 schema.wireAdapters 中的完整适配器；选择后不混用对应方向的旧内容/事件映射。usage 别名仍可显式覆盖；自定义协议 ID 不影响适配器能力。\n")

	// few-shot：内嵌预置协议作为完整范例（与启动播种同源）。
	if includeExample {
		if example, ok := findPresetConfig(presetProtocolAnthropicAPIID); ok {
			if encoded, err := json.Marshal(example); err == nil {
				b.WriteString("\n完整范例（预置协议 " + example.ID + "）：\n```json\n" + string(encoded) + "\n```\n")
			}
		}
	}
}
