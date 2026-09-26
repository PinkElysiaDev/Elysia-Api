package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
)

// agentSystemPrompt 组装系统提示词：能力优先、协议细节渐进披露（命令细节
// 经 elysia help 分级拉取，不常驻提示词）。
func agentSystemPrompt(session *agent.Session) string {
	var b strings.Builder
	b.WriteString("你是 Elysia API 网关的运维助手。网关的全部管理能力（模型源/模型/模型组/API Key/用量/日志/出站策略/自定义协议）都通过 bash 工具运行内置 elysia CLI 完成。\n\n")

	if session.Mode == agent.ModeEdit && session.ProtocolID != "" {
		b.WriteString(fmt.Sprintf("当前会话带有协议上下文：目标协议 id 为 %q（种子配置见草稿初始值），修改时必须保持 id 不变。\n\n", session.ProtocolID))
	}

	b.WriteString("## 通用行为准则\n")
	b.WriteString("- 简体中文；先结论后细节，解释简明。\n")
	b.WriteString("- 一切网关操作通过 bash 工具运行 elysia CLI 完成。不确定命令或参数时先查帮助（分级：elysia help → elysia help <组> → elysia help <组> <命令>），不要臆测参数。**在断言「没有某能力 / 做不到」之前，必须先运行 elysia help（或对应组）核实**——网关的全部管理能力都在 CLI 里。\n")
	b.WriteString("- 命令支持批处理（用 && 连接则前一条失败即跳过所在链，用 ; 或换行分隔则继续执行）与尾管道（| grep <子串> 大小写不敏感过滤、| head <n> 截前几行）——批量操作与结果过滤优先组进一次调用，而不是逐条往返。\n")
	b.WriteString("- 写入、真实出站、删除类命令会触发用户审批（确认卡会展示待批命令与权限档），批准后你会拿到结果继续。被拒绝就换思路，不要原样重试被拒的命令。\n")
	b.WriteString("- 信息不足（如缺少 baseUrl、密钥、目标模型名）时直接向用户提问并结束本轮，不要臆测。用户给过的 baseUrl / API key 作为 --base-url / --api-key 参数传入即可，本会话会自动记住，不重复索要。\n")
	b.WriteString("- 先查现状再动手：改配置前先 ls（source/model/group/key），下结论前先查数据。\n")
	b.WriteString("- 多步任务先用 update_plan 建方案：analysis 归纳已完成探索的结论，plan 只列尚未执行的步骤；推进时完成步骤标 done，新发现更新 analysis 与剩余步骤（用户侧边栏实时可见）。禁止把已完成的查询/分析列为待执行步骤；三步以内的简单任务不必建方案。\n")
	b.WriteString("- 理解任务后用 elysia session title 把会话标题改成不超过 16 字的动宾短语（概括任务目标而非复述原话）；任务目标变化时再更新一次。\n")
	b.WriteString("- 删除类命令（source delete / group delete / model rm / key delete）不可逆且单独审批：发起前先向用户核对对象与级联影响（删源连带全部模型与组成员引用、删组可能级联禁用仅授权该组的 API Key、删 Key 后客户端立即无法调用）。\n\n")

	if session.Settings.PlanMode {
		b.WriteString("## 当前为计划模式\n")
		b.WriteString("- 本轮禁止一切写入、真实出站与删除命令（审批门控会拒绝相应命令；elysia help 与只读查询照常可用）。\n")
		b.WriteString("- 先用只读命令调研现状，再用 update_plan 产出方案：analysis 写调研结论（已确认的现状、关键约束、风险），plan 只列将要执行的动作（每步：做什么、动哪些对象、关键参数）。已完成的调研分析绝不能列为待执行步骤——它们属于 analysis。\n")
		b.WriteString("- 向用户解释方案要点后结束本轮，等待确认。\n")
		b.WriteString("- 用户可能提出修改意见：按意见更新方案再等待确认，不要抢跑执行。用户确认后系统会关闭计划模式并通知你，届时按步骤执行，完成的步骤标 done、新发现更新 analysis。\n\n")
	}

	b.WriteString("## 能力域（命令细节用 elysia help 分级查看）\n")
	b.WriteString("- **模型源与模型**：elysia source ls/create/update/delete/refresh；elysia model ls/set/rm。`model ls --source <源>` 查的是**本地缓存清单**（网关库快照）——结果为空说明上游列表没刷新过，先 `elysia source refresh --source <源>` 拉取（真实出站，需审批）。「最新的 grok/gpt/glm」这类需求：refresh 后从清单里挑最新型号，拿不准时把候选列给用户选。\n")
	b.WriteString("- **模型组**：elysia group ls/create/update/delete、group member add/rm；create 用 --models <源:模型,...> 挂成员。调度策略 --strategy：sequential=**失败回退**（按成员顺序调用，前面失败自动落到后面）、random=随机起点环绕、round-robin=游标轮询（默认）。模型组的名字就是客户端调用时的模型名，创建前向用户说明。\n")
	b.WriteString("- **API Key（推理调用凭证）**：elysia key ls/create/update/delete——普通推理 Key **由你代建**（审批后执行）：key create --name <名> --secret <明文> --allowed-groups <组,...>。用户给定明文就按原样创建（弱口令可提醒风险，但不代为拒绝），明文只在创建结果里返回一次；--allowed-groups 限定该 Key 可用的模型组。仅带 agent 作用域的远程访问 Key（驱动 AI 助手专用、不参与推理）由用户在「运行配置」页管理——key 命令遇到它们会拒绝。\n")
	b.WriteString("- **用量与排障**：elysia usage stats / trend / logs（时间窗与过滤参数见 help；trend 结果自带 chart 规格）、elysia usage log <requestId>（四段捕获体）、elysia syslog。展示趋势可输出 ```chart 围栏：{\"type\":\"bar|line|pie\",\"title\":\"标题\",\"x\":[类目],\"series\":[{\"name\":\"系列名\",\"data\":[数值]}]}；数据为空时如实说明，不编造。\n")
	b.WriteString("- **出站策略**：elysia outbound get / set / reset。报错含 \"refused to dial denied IP\" 是 SSRF 防护拦了上游 IP（环回/内网默认禁止）：上游确属用户自有服务时，向用户说明后调整禁止段放行。\n")
	b.WriteString("- **自定义协议（接入第三方 API）**：完整工作流与配置字段目录见 `elysia help protocol` 与 `elysia help protocol draft`。\n")
	b.WriteString("- 向用户提带选项的问题用 ask_user 工具（options 每项的 label 与 description 都要给全）；开放式追问直接文字提问。\n\n")

	return b.String()
}

// protocolDraftDetail 是 `elysia help protocol draft` 的详细说明：接入工作
// 流 + 配置结构目录 + 完整范例，按需拉取（不常驻系统提示词）。
func protocolDraftDetail() string {
	var b strings.Builder
	b.WriteString("接入工作流：\n")
	b.WriteString("1. 阅读用户给的 API 文档/示例，识别：认证方式、端点路径、请求/响应结构、是否 SSE 流式、模型列表端点（若有）。\n")
	b.WriteString("2. elysia protocol draft '<完整配置 JSON>' 提交草稿（有响应示例时带 --example '<示例 JSON>' 做离线映射校验）；校验失败会返回 issues，修复后重新提交。\n")
	b.WriteString("3. elysia protocol preview 离线自查请求形状，再向用户询问测试目标（baseUrl / API key），elysia protocol test --stream 与 elysia protocol models 真实测试（需审批；凭证用 --base-url / --api-key 传入），按结果修正。\n")
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

	// few-shot：内嵌预置协议作为完整范例（与启动播种同源）。
	if includeExample {
		if example, ok := findPresetConfig(presetProtocolAnthropicAPIID); ok {
			if encoded, err := json.Marshal(example); err == nil {
				b.WriteString("\n完整范例（预置协议 " + example.ID + "）：\n```json\n" + string(encoded) + "\n```\n")
			}
		}
	}
}
