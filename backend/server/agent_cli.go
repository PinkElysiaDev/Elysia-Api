package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elysia-api/backend/agent"
)

// elysia CLI：把既有工具面收敛为单一 bash 入口的命令路由层。命令表里的
// 每条命令委托一个既有 Tool 实现（行为与直接调用完全一致），权限/风险/
// 超时均取自目标工具——CLI 只是外观，不引入第二套语义。
//
// 支持的脚本语法（轻量 shell 子集）：
//   - 引号：'...'（原样）与 "..."（支持 \" 转义）；
//   - flag：--name value / --name=value；布尔 flag 单独出现即 true；
//     列表型 flag 可重复出现或逗号分隔；
//   - 批处理：`&&` 失败即停、`;` 或换行 继续执行；
//   - 尾管道：| grep <子串> 与 | head <n>。

const (
	cliOutputBudgetBytes = 32 * 1024
	cliName              = "elysia"
)

// ---- 解析 ----

// cliStatement 是一条待执行语句（命令 + 尾管道）。
type cliStatement struct {
	raw     string
	args    []string
	grep    string
	head    int
	hasGrep bool
}

// cliTokenize 把单条命令切成参数（处理引号与转义）。
func cliTokenize(line string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == '\\' && inDouble && i+1 < len(line) && (line[i+1] == '"' || line[i+1] == '\\'):
			current.WriteByte(line[i+1])
			i++
		case (ch == ' ' || ch == '\t') && !inSingle && !inDouble:
			flush()
		default:
			current.WriteByte(ch)
		}
	}
	flush()
	if inSingle || inDouble {
		return nil, fmt.Errorf("引号未闭合")
	}
	return tokens, nil
}

// cliSegment 是一条语句及其分隔语义：mustSucceed=true（`&&` 分隔）时
// 前一条失败即停止整批。
type cliSegment struct {
	raw         string
	mustSucceed bool
}

// cliSplitStatements 按顶层 `&&` / `;` / 换行切分脚本（引号内不切分）。
func cliSplitStatements(script string) ([]cliSegment, error) {
	var parts []cliSegment
	var current strings.Builder
	inSingle, inDouble := false, false
	flush := func(mustSucceed bool) {
		if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
			parts = append(parts, cliSegment{raw: trimmed, mustSucceed: mustSucceed})
		}
		current.Reset()
	}
	for i := 0; i < len(script); i++ {
		ch := script[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		}
		if inSingle || inDouble {
			current.WriteByte(ch)
			continue
		}
		if ch == ';' || ch == '\n' {
			flush(false)
			continue
		}
		if ch == '&' && i+1 < len(script) && script[i+1] == '&' {
			// `&&`：本条 mustSucceed、失败即停。
			flush(true)
			i++
			continue
		}
		current.WriteByte(ch)
	}
	flush(false)
	if inSingle || inDouble {
		return nil, fmt.Errorf("引号未闭合")
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("空命令")
	}
	return parts, nil
}

// cliParseStatement 解析单条语句：切管道（仅支持 grep/head）、切参数。
func cliParseStatement(raw string) (cliStatement, error) {
	statement := cliStatement{raw: raw}
	segments := splitPipe(raw)
	tokens, err := cliTokenize(segments[0])
	if err != nil {
		return statement, err
	}
	if len(tokens) == 0 || tokens[0] != cliName {
		return statement, fmt.Errorf("命令必须以 %s 开头（当前 %q）", cliName, firstWord(segments[0]))
	}
	statement.args = tokens[1:]
	for _, pipe := range segments[1:] {
		pipeTokens, err := cliTokenize(pipe)
		if err != nil {
			return statement, err
		}
		if len(pipeTokens) == 0 {
			return statement, fmt.Errorf("空管道段")
		}
		switch pipeTokens[0] {
		case "grep":
			if len(pipeTokens) != 2 {
				return statement, fmt.Errorf("grep 管道只接受一个子串参数")
			}
			statement.hasGrep = true
			statement.grep = pipeTokens[1]
		case "head":
			if len(pipeTokens) != 2 {
				return statement, fmt.Errorf("head 管道只接受一个行数参数")
			}
			n, err := strconv.Atoi(pipeTokens[1])
			if err != nil || n <= 0 {
				return statement, fmt.Errorf("head 行数必须是正整数")
			}
			statement.head = n
		default:
			return statement, fmt.Errorf("只支持 | grep <子串> 与 | head <n> 管道（不支持 %q）", pipeTokens[0])
		}
	}
	return statement, nil
}

// splitPipe 按顶层 | 切分（引号内不切）。
func splitPipe(raw string) []string {
	var segments []string
	var current strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		}
		if ch == '|' && !inSingle && !inDouble {
			segments = append(segments, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	return append(segments, current.String())
}

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// cliInvocation 是一条命令解析后的调用形状。
type cliInvocation struct {
	command *cliCommand
	args    []string          // 位置参数
	flags   map[string]string // flag 名（不含 --）→ 值；布尔为 "true"；重复/逗号列表已合并
	present map[string]bool
}

// Has 报告 flag 是否出现。
func (inv *cliInvocation) Has(name string) bool { return inv.present[name] }

// Str 返回 flag 字符串值（未出现为空串）。
func (inv *cliInvocation) Str(name string) string { return inv.flags[name] }

// List 返回列表值（逗号拆分）。
func (inv *cliInvocation) List(name string) []string {
	raw, ok := inv.flags[name]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	items := []string{}
	for _, item := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// setStr/setBool/setInt/setList 把 flag 值写进参数 map（出现才写，保持
// 目标工具的「未传=保留」语义）。返回值报告是否出现。
func (inv *cliInvocation) setStr(m map[string]any, flag, key string) bool {
	if !inv.present[flag] {
		return false
	}
	m[key] = inv.flags[flag]
	return true
}

func (inv *cliInvocation) setBool(m map[string]any, flag, key string) bool {
	if !inv.present[flag] {
		return false
	}
	m[key] = inv.flags[flag] == "true" || inv.flags[flag] == "1" || inv.flags[flag] == ""
	return true
}

func (inv *cliInvocation) setInt(m map[string]any, flag, key string) (bool, error) {
	if !inv.present[flag] {
		return false, nil
	}
	value, err := strconv.Atoi(inv.flags[flag])
	if err != nil {
		return true, fmt.Errorf("--%s 需要整数（当前 %q）", flag, inv.flags[flag])
	}
	m[key] = value
	return true, nil
}

func (inv *cliInvocation) setList(m map[string]any, flag, key string) bool {
	if !inv.present[flag] {
		return false
	}
	m[key] = inv.List(flag)
	return true
}

// setJSON 把 flag/位置参数里的 JSON 文本原样放入参数 map。
func (inv *cliInvocation) setJSONText(m map[string]any, key, text string) error {
	trimmed := strings.TrimSpace(text)
	if !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("%s 需要合法 JSON 文本", key)
	}
	m[key] = json.RawMessage(trimmed)
	return nil
}

// ---- 命令表 ----

// cliFlagSpec 声明一个 flag 的形态与 help 文案。
type cliFlagSpec struct {
	name    string
	usage   string
	boolean bool
	list    bool
}

// cliPositionalSpec 声明一个位置参数。
type cliPositionalSpec struct {
	name  string
	usage string
}

// cliCommand 是命令表的一条：路径 + flag 规格 + 参数映射器 + 目标工具。
type cliCommand struct {
	group       string
	name        string // 组内命令名（二级命令用空格连接，如 "member add"）
	summary     string // help 与审批摘要用的一句话
	usage       string // 用法行（help 展示）
	example     string
	flags       []cliFlagSpec
	positionals []cliPositionalSpec
	tool        func(s *Server) agent.Tool
	mapper      func(inv *cliInvocation) (map[string]any, error)
}

// Path 返回完整命令路径（如 "source create"）。
func (c *cliCommand) Path() string { return c.group + " " + c.name }

func (c *cliCommand) flagByName(name string) *cliFlagSpec {
	for i := range c.flags {
		if c.flags[i].name == name {
			return &c.flags[i]
		}
	}
	return nil
}

// cliCommandGroups 是命令表（顺序即 help 顺序）。
func cliCommandTable() []*cliCommand {
	table := []*cliCommand{}
	table = append(table,
		// ---- source ----
		&cliCommand{group: "source", name: "ls", summary: "列出全部模型源（密钥脱敏）",
			usage: "elysia source ls", example: "elysia source ls",
			tool:   func(s *Server) agent.Tool { return &listSourcesTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) { return map[string]any{}, nil }},
		&cliCommand{group: "source", name: "create", summary: "创建模型源（需审批）",
			usage:   "elysia source create --name <名> --base-url <URL> [--platform openai|anthropic|gemini|responses|custom:<id>] [--api-key <key>] [--auto-fetch] [--manual-models a,b] [--fetch-base-url <URL>]",
			example: `elysia source create --name 主源 --base-url https://api.example.com --api-key sk-xxx`,
			flags: []cliFlagSpec{
				{"name", "源名称（显示用）", false, false},
				{"base-url", "上游 baseUrl（http/https）", false, false},
				{"platform", "openai（默认）/anthropic/gemini/responses/custom:<协议ID>", false, false},
				{"api-key", "API key（加密存储）", false, false},
				{"auto-fetch", "自动拉取模型列表", true, false},
				{"manual-models", "手动模型名列表（逗号分隔）", false, true},
				{"fetch-base-url", "模型列表拉取地址（缺省同 base-url）", false, false},
			},
			tool: func(s *Server) agent.Tool { return &createSourceTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "name", "name") {
					return nil, cliMissing("name")
				}
				if !inv.setStr(m, "base-url", "baseUrl") {
					return nil, cliMissing("base-url")
				}
				inv.setStr(m, "platform", "platform")
				inv.setStr(m, "api-key", "apiKey")
				inv.setBool(m, "auto-fetch", "autoFetchModels")
				inv.setList(m, "manual-models", "manualModels")
				inv.setStr(m, "fetch-base-url", "fetchBaseUrl")
				return m, nil
			}},
		&cliCommand{group: "source", name: "update", summary: "修改模型源（需审批；api-key 留空=保留）",
			usage:   "elysia source update --source <id|名> [--enabled] [--name <名>] [--base-url <URL>] [--platform <平台>] [--api-key <新key>] [--auto-fetch[=false]] [--manual-models a,b]",
			example: `elysia source update --source 主源 --enabled=false`,
			flags: []cliFlagSpec{
				{"source", "源 id 或名称", false, false},
				{"enabled", "启停", true, false},
				{"name", "改名", false, false},
				{"base-url", "换 baseUrl", false, false},
				{"platform", "换平台", false, false},
				{"api-key", "新 API key（留空=保留原值）", false, false},
				{"auto-fetch", "自动拉取开关", true, false},
				{"manual-models", "整体替换手动模型列表", false, true},
			},
			tool: func(s *Server) agent.Tool { return &updateSourceTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "source", "source") {
					return nil, cliMissing("source")
				}
				inv.setBool(m, "enabled", "enabled")
				inv.setStr(m, "name", "name")
				inv.setStr(m, "base-url", "baseUrl")
				inv.setStr(m, "platform", "platform")
				inv.setStr(m, "api-key", "apiKey")
				inv.setBool(m, "auto-fetch", "autoFetchModels")
				inv.setList(m, "manual-models", "manualModels")
				return m, nil
			}},
		&cliCommand{group: "source", name: "delete", summary: "删除模型源（不可逆，需审批；级联删模型与组引用）",
			usage:   "elysia source delete --source <id|名>",
			example: `elysia source delete --source 主源`,
			flags:   []cliFlagSpec{{"source", "源 id 或名称", false, false}},
			tool:    func(s *Server) agent.Tool { return &deleteSourceTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "source", "source") {
					return nil, cliMissing("source")
				}
				return m, nil
			}},
		&cliCommand{group: "source", name: "refresh", summary: "从上游拉取模型列表（真实出站，需审批）",
			usage:   "elysia source refresh --source <id|名>",
			example: `elysia source refresh --source 主源`,
			flags:   []cliFlagSpec{{"source", "源 id 或名称", false, false}},
			tool:    func(s *Server) agent.Tool { return &refreshSourceTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "source", "source") {
					return nil, cliMissing("source")
				}
				return m, nil
			}},
	)
	table = append(table,
		// ---- model ----
		&cliCommand{group: "model", name: "ls", summary: "查询模型清单（可按源过滤）",
			usage:   "elysia model ls [--source <id|名>] [--search <子串>] [--limit <n>]",
			example: `elysia model ls --source 主源 --search gpt --limit 20`,
			flags: []cliFlagSpec{
				{"source", "源 id 或名称", false, false},
				{"search", "名称模糊匹配", false, false},
				{"limit", "返回条数（默认 50，上限 200）", false, false},
			},
			tool: func(s *Server) agent.Tool { return &listModelsTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				inv.setStr(m, "source", "source")
				inv.setStr(m, "search", "search")
				if _, err := inv.setInt(m, "limit", "limit"); err != nil {
					return nil, err
				}
				return m, nil
			}},
		&cliCommand{group: "model", name: "set", summary: "修改单个模型（需审批）",
			usage:   "elysia model set --source <id|名> --model <模型id> [--name <名>] [--type <类型>] [--max-tokens <n>] [--vision] [--tools] [--structured] [--thinking <模式>] [--enabled[=false]]",
			example: `elysia model set --source 主源 --model gpt-4o --vision --enabled=false`,
			flags: []cliFlagSpec{
				{"source", "源 id 或名称", false, false},
				{"model", "模型 id", false, false},
				{"name", "改名", false, false},
				{"type", "类型", false, false},
				{"max-tokens", "maxTokens", false, false},
				{"vision", "视觉能力标记", true, false},
				{"tools", "工具能力标记", true, false},
				{"structured", "结构化输出标记", true, false},
				{"thinking", "思考模式", false, false},
				{"enabled", "启停", true, false},
			},
			tool: func(s *Server) agent.Tool { return &updateModelTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "source", "source") {
					return nil, cliMissing("source")
				}
				if !inv.setStr(m, "model", "model") {
					return nil, cliMissing("model")
				}
				inv.setStr(m, "name", "name")
				inv.setStr(m, "type", "type")
				if _, err := inv.setInt(m, "max-tokens", "maxTokens"); err != nil {
					return nil, err
				}
				inv.setBool(m, "vision", "visionCapable")
				inv.setBool(m, "tools", "toolsCapable")
				inv.setBool(m, "structured", "structuredOutput")
				inv.setStr(m, "thinking", "thinkingMode")
				inv.setBool(m, "enabled", "enabled")
				return m, nil
			}},
		&cliCommand{group: "model", name: "rm", summary: "删除单个模型（不可逆，需审批）",
			usage:   "elysia model rm --source <id|名> --model <模型id>",
			example: `elysia model rm --source 主源 --model gpt-4o`,
			flags: []cliFlagSpec{
				{"source", "源 id 或名称", false, false},
				{"model", "模型 id", false, false},
			},
			tool: func(s *Server) agent.Tool { return &deleteModelTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "source", "source") {
					return nil, cliMissing("source")
				}
				if !inv.setStr(m, "model", "model") {
					return nil, cliMissing("model")
				}
				return m, nil
			}},
	)
	table = append(table,
		// ---- group ----
		&cliCommand{group: "group", name: "ls", summary: "列出全部模型组及成员",
			usage: "elysia group ls", example: "elysia group ls",
			tool:   func(s *Server) agent.Tool { return &listGroupsTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) { return map[string]any{}, nil }},
		&cliCommand{group: "group", name: "create", summary: "创建模型组（需审批）",
			usage:   "elysia group create --name <组名> [--models <src:model,...>] [--strategy round-robin|random|sequential] [--max-retries <n>] [--enabled[=false]] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]",
			example: `elysia group create --name 主力 --models s1:gpt-4o,s1:gpt-4o-mini`,
			flags: []cliFlagSpec{
				{"name", "组名（客户端调用时用的模型名）", false, false},
				{"models", "成员模型引用（sourceId:modelId 或模型名，逗号分隔）", false, true},
				{"strategy", "round-robin（默认）/random/sequential", false, false},
				{"max-retries", "失败重试次数（默认 3）", false, false},
				{"enabled", "默认 true", true, false},
				{"max-concurrency", "并发上限（0=不限）", false, false},
				{"daily-limit-requests", "每日请求上限（0=不限）", false, false},
				{"daily-limit-tokens", "每日 token 上限（0=不限）", false, false},
			},
			tool: func(s *Server) agent.Tool { return &createGroupTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "name", "name") {
					return nil, cliMissing("name")
				}
				inv.setList(m, "models", "models")
				inv.setStr(m, "strategy", "strategy")
				if _, err := inv.setInt(m, "max-retries", "maxRetries"); err != nil {
					return nil, err
				}
				inv.setBool(m, "enabled", "enabled")
				if _, err := inv.setInt(m, "max-concurrency", "maxConcurrency"); err != nil {
					return nil, err
				}
				if _, err := inv.setInt(m, "daily-limit-requests", "dailyLimitMaxRequests"); err != nil {
					return nil, err
				}
				if _, err := inv.setInt(m, "daily-limit-tokens", "dailyLimitMaxTokens"); err != nil {
					return nil, err
				}
				return m, nil
			}},
		&cliCommand{group: "group", name: "update", summary: "修改模型组（需审批；成员增删/策略/限额）",
			usage:   "elysia group update --group <组名|id> [--add-models <列表>] [--remove-models <列表>] [--enabled[=false]] [--strategy <策略>] [--max-retries <n>] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]",
			example: `elysia group update --group 主力 --add-models s1:o1 --enabled=false`,
			flags: []cliFlagSpec{
				{"group", "组名或 id", false, false},
				{"add-models", "追加成员", false, true},
				{"remove-models", "移除成员", false, true},
				{"enabled", "启停", true, false},
				{"strategy", "调度策略", false, false},
				{"max-retries", "重试次数", false, false},
				{"max-concurrency", "并发上限", false, false},
				{"daily-limit-requests", "每日请求上限", false, false},
				{"daily-limit-tokens", "每日 token 上限", false, false},
			},
			tool: func(s *Server) agent.Tool { return &updateGroupTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "group", "group") {
					return nil, cliMissing("group")
				}
				inv.setList(m, "add-models", "addModels")
				inv.setList(m, "remove-models", "removeModels")
				inv.setBool(m, "enabled", "enabled")
				inv.setStr(m, "strategy", "strategy")
				if _, err := inv.setInt(m, "max-retries", "maxRetries"); err != nil {
					return nil, err
				}
				if _, err := inv.setInt(m, "max-concurrency", "maxConcurrency"); err != nil {
					return nil, err
				}
				if _, err := inv.setInt(m, "daily-limit-requests", "dailyLimitMaxRequests"); err != nil {
					return nil, err
				}
				if _, err := inv.setInt(m, "daily-limit-tokens", "dailyLimitMaxTokens"); err != nil {
					return nil, err
				}
				return m, nil
			}},
		&cliCommand{group: "group", name: "delete", summary: "删除模型组（不可逆，需审批；可能级联禁用 Key）",
			usage:   "elysia group delete --group <组名|id>",
			example: `elysia group delete --group 退役组`,
			flags:   []cliFlagSpec{{"group", "组名或 id", false, false}},
			tool:    func(s *Server) agent.Tool { return &deleteGroupTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "group", "group") {
					return nil, cliMissing("group")
				}
				return m, nil
			}},
		&cliCommand{group: "group", name: "member add", summary: "向模型组追加成员（需审批）",
			usage:   "elysia group member add --group <组名|id> --models <列表>",
			example: `elysia group member add --group 主力 --models s1:o1,s1:o2`,
			flags: []cliFlagSpec{
				{"group", "组名或 id", false, false},
				{"models", "成员引用（sourceId:modelId 或模型名）", false, true},
			},
			tool: func(s *Server) agent.Tool { return &updateGroupTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "group", "group") {
					return nil, cliMissing("group")
				}
				models := inv.List("models")
				if len(models) == 0 {
					return nil, cliMissing("models")
				}
				m["addModels"] = models
				return m, nil
			}},
		&cliCommand{group: "group", name: "member rm", summary: "从模型组移除成员（需审批）",
			usage:   "elysia group member rm --group <组名|id> --models <列表>",
			example: `elysia group member rm --group 主力 --models s1:o2`,
			flags: []cliFlagSpec{
				{"group", "组名或 id", false, false},
				{"models", "成员引用", false, true},
			},
			tool: func(s *Server) agent.Tool { return &updateGroupTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "group", "group") {
					return nil, cliMissing("group")
				}
				models := inv.List("models")
				if len(models) == 0 {
					return nil, cliMissing("models")
				}
				m["removeModels"] = models
				return m, nil
			}},
	)
	table = append(table,
		// ---- key ----
		&cliCommand{group: "key", name: "ls", summary: "查询 API Key 列表（脱敏）",
			usage: "elysia key ls", example: "elysia key ls",
			tool:   func(s *Server) agent.Tool { return &listAPIKeysTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) { return map[string]any{}, nil }},
		&cliCommand{group: "key", name: "create", summary: "创建推理 API Key（需审批；secret 留空自动生成，明文仅返回一次）",
			usage:   "elysia key create --name <名> [--secret <明文>] [--allowed-groups <组,...>] [--enabled[=false]]",
			example: `elysia key create --name mobile-app --allowed-groups 主力`,
			flags: []cliFlagSpec{
				{"name", "Key 名称（主键，创建后不可改）", false, false},
				{"secret", "Key 明文；留空自动生成随机值", false, false},
				{"allowed-groups", "允许访问的模型组（逗号分隔；空=不限制）", false, true},
				{"enabled", "默认 true", true, false},
			},
			tool: func(s *Server) agent.Tool { return &createAPIKeyTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "name", "name") {
					return nil, cliMissing("name")
				}
				inv.setStr(m, "secret", "secret")
				inv.setList(m, "allowed-groups", "allowedGroups")
				inv.setBool(m, "enabled", "enabled")
				return m, nil
			}},
		&cliCommand{group: "key", name: "update", summary: "修改 API Key（需审批；new-secret 留空=保留；远程访问 Key 拒绝）",
			usage:   "elysia key update --name <名> [--enabled[=false]] [--allowed-groups <组,...>] [--new-secret <新明文>]",
			example: `elysia key update --name mobile-app --allowed-groups 主力,备用`,
			flags: []cliFlagSpec{
				{"name", "Key 名称", false, false},
				{"enabled", "启停", true, false},
				{"allowed-groups", "整体替换允许访问的模型组", false, true},
				{"new-secret", "新明文；留空保留原值", false, false},
			},
			tool: func(s *Server) agent.Tool { return &updateAPIKeyTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "name", "name") {
					return nil, cliMissing("name")
				}
				inv.setBool(m, "enabled", "enabled")
				inv.setList(m, "allowed-groups", "allowedGroups")
				inv.setStr(m, "new-secret", "newSecret")
				return m, nil
			}},
		&cliCommand{group: "key", name: "delete", summary: "删除 API Key（不可逆，需审批；远程访问 Key 拒绝）",
			usage:   "elysia key delete --name <名>",
			example: `elysia key delete --name mobile-app`,
			flags:   []cliFlagSpec{{"name", "Key 名称", false, false}},
			tool:    func(s *Server) agent.Tool { return &deleteAPIKeyTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "name", "name") {
					return nil, cliMissing("name")
				}
				return m, nil
			}},
	)
	table = append(table,
		// ---- protocol ----
		&cliCommand{group: "protocol", name: "draft", summary: "写入/更新协议配置草稿（立即校验并离线验证）",
			usage:   "elysia protocol draft '<完整配置 JSON>' [--example '<响应示例 JSON>']",
			example: `elysia protocol draft '{"id":"my-api","request":{...}}' --example '{"text":"hi"}'`,
			positionals: []cliPositionalSpec{
				{"config", "完整 CustomProtocolConfig JSON（建议用单引号包裹）"},
			},
			flags: []cliFlagSpec{
				{"example", "上游响应示例 JSON，用于离线检验 response 映射", false, false},
			},
			tool: func(s *Server) agent.Tool { return &updateDraftTool{} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if len(inv.args) == 0 || strings.TrimSpace(inv.args[0]) == "" {
					return nil, fmt.Errorf("缺少位置参数 <config>（完整协议配置 JSON）")
				}
				if err := inv.setJSONText(m, "config", inv.args[0]); err != nil {
					return nil, err
				}
				if inv.Has("example") {
					if err := inv.setJSONText(m, "exampleResponse", inv.Str("example")); err != nil {
						return nil, err
					}
				}
				return m, nil
			}},
		&cliCommand{group: "protocol", name: "preview", summary: "离线渲染草稿请求（不发送）",
			usage:   "elysia protocol preview [--sample '<样例 Maheshvara 请求 JSON>']",
			example: `elysia protocol preview`,
			flags:   []cliFlagSpec{{"sample", "自定义样例请求 JSON", false, false}},
			tool:    func(s *Server) agent.Tool { return &previewRequestTool{} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if inv.Has("sample") {
					if err := inv.setJSONText(m, "sampleRequest", inv.Str("sample")); err != nil {
						return nil, err
					}
				}
				return m, nil
			}},
		&cliCommand{group: "protocol", name: "test", summary: "向真实上游发送一次测试请求（需审批）",
			usage:   "elysia protocol test [--base-url <URL>] [--api-key <key>] [--stream] [--sample '<样例请求 JSON>']",
			example: `elysia protocol test --base-url https://api.example.com --api-key sk-xxx`,
			flags: []cliFlagSpec{
				{"base-url", "用户提供的上游 baseUrl（缺省用会话已记住的）", false, false},
				{"api-key", "用户提供的 API key（缺省用会话已记住的）", false, false},
				{"stream", "按流式（SSE）测试", true, false},
				{"sample", "自定义样例请求 JSON", false, false},
			},
			tool: func(s *Server) agent.Tool { return &testUpstreamTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				inv.setStr(m, "base-url", "baseUrl")
				inv.setStr(m, "api-key", "apiKey")
				inv.setBool(m, "stream", "stream")
				if inv.Has("sample") {
					if err := inv.setJSONText(m, "sampleRequest", inv.Str("sample")); err != nil {
						return nil, err
					}
				}
				return m, nil
			}},
		&cliCommand{group: "protocol", name: "models", summary: "按草稿 models 配置试拉上游模型列表（需审批）",
			usage:   "elysia protocol models [--base-url <URL>] [--api-key <key>]",
			example: `elysia protocol models --base-url https://api.example.com`,
			flags: []cliFlagSpec{
				{"base-url", "上游 baseUrl（缺省用会话已记住的）", false, false},
				{"api-key", "API key（缺省用会话已记住的）", false, false},
			},
			tool: func(s *Server) agent.Tool { return &testModelsTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				inv.setStr(m, "base-url", "baseUrl")
				inv.setStr(m, "api-key", "apiKey")
				return m, nil
			}},
		&cliCommand{group: "protocol", name: "save", summary: "把当前草稿保存为正式协议（需审批）",
			usage: "elysia protocol save", example: "elysia protocol save",
			tool:   func(s *Server) agent.Tool { return &saveProtocolTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) { return map[string]any{}, nil }},
		&cliCommand{group: "protocol", name: "read", summary: "读取已保存协议的完整配置",
			usage:   "elysia protocol read --id <协议id>",
			example: `elysia protocol read --id anthropic-api`,
			flags:   []cliFlagSpec{{"id", "协议 id（含内置预置协议）", false, false}},
			tool:    func(s *Server) agent.Tool { return &readProtocolTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.setStr(m, "id", "id") {
					return nil, cliMissing("id")
				}
				return m, nil
			}},
	)
	table = append(table,
		// ---- usage ----
		&cliCommand{group: "usage", name: "stats", summary: "查询用量汇总与模型分布",
			usage:   "elysia usage stats [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>]",
			example: `elysia usage stats --days 7 --group 主力`,
			flags:   cliWindowFlags(),
			tool:    func(s *Server) agent.Tool { return &usageStatsTool{server: s} },
			mapper:  cliWindowMapper},
		&cliCommand{group: "usage", name: "trend", summary: "查询用量按日趋势",
			usage:   "elysia usage trend [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>]",
			example: `elysia usage trend --days 30`,
			flags:   cliWindowFlags(),
			tool:    func(s *Server) agent.Tool { return &usageTrendTool{server: s} },
			mapper:  cliWindowMapper},
		&cliCommand{group: "usage", name: "logs", summary: "查询调用日志列表（可过滤错误）",
			usage:   "elysia usage logs [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>] [--status success|failed] [--code <状态码>] [--limit <n>]",
			example: `elysia usage logs --days 1 --status failed --limit 20`,
			flags: append(cliWindowFlags(),
				cliFlagSpec{"status", "success | failed", false, false},
				cliFlagSpec{"code", "精确状态码", false, false},
				cliFlagSpec{"limit", "返回条数（默认 20，最大 100）", false, false}),
			tool: func(s *Server) agent.Tool { return &usageLogsTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m, err := cliWindowMapper(inv)
				if err != nil {
					return nil, err
				}
				inv.setStr(m, "status", "status")
				if _, err := inv.setInt(m, "code", "statusCode"); err != nil {
					return nil, err
				}
				if _, err := inv.setInt(m, "limit", "limit"); err != nil {
					return nil, err
				}
				return m, nil
			}},
		&cliCommand{group: "usage", name: "log", summary: "读取单条调用日志详情（含四段捕获体）",
			usage:   "elysia usage log <requestId>",
			example: `elysia usage log req-123`,
			positionals: []cliPositionalSpec{
				{"requestId", "调用日志的 requestId"},
			},
			tool: func(s *Server) agent.Tool { return &usageLogDetailTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if len(inv.args) == 0 || strings.TrimSpace(inv.args[0]) == "" {
					return nil, fmt.Errorf("缺少位置参数 <requestId>")
				}
				m["requestId"] = inv.args[0]
				return m, nil
			}},
		&cliCommand{group: "syslog", name: "", summary: "查询系统日志",
			usage:   "elysia syslog [--level info|warn|error] [--limit <n>]",
			example: `elysia syslog --level error --limit 50`,
			flags: []cliFlagSpec{
				{"level", "info | warn | error（缺省全部）", false, false},
				{"limit", "返回条数（默认 30，最大 100）", false, false},
			},
			tool: func(s *Server) agent.Tool { return &systemLogsTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				inv.setStr(m, "level", "level")
				if _, err := inv.setInt(m, "limit", "limit"); err != nil {
					return nil, err
				}
				return m, nil
			}},
	)
	table = append(table,
		// ---- outbound ----
		&cliCommand{group: "outbound", name: "get", summary: "查看出站禁止 IP 段（SSRF 防护）",
			usage: "elysia outbound get", example: "elysia outbound get",
			tool:   func(s *Server) agent.Tool { return &outboundPolicyTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) { return map[string]any{}, nil }},
		&cliCommand{group: "outbound", name: "set", summary: "整体替换出站禁止段（需审批；高影响）",
			usage:   "elysia outbound set --ranges <CIDR,...>   # 空列表 = 全放行",
			example: `elysia outbound set --ranges 10.0.0.0/8,172.16.0.0/12`,
			flags:   []cliFlagSpec{{"ranges", "禁止段 CIDR 列表（逗号分隔；空=放行所有）", false, true}},
			tool:    func(s *Server) agent.Tool { return &outboundPolicyTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if !inv.present["ranges"] {
					return nil, cliMissing("ranges")
				}
				m["ranges"] = inv.List("ranges")
				return m, nil
			}},
		&cliCommand{group: "outbound", name: "reset", summary: "恢复出站禁止段为预置默认（需审批）",
			usage: "elysia outbound reset", example: "elysia outbound reset",
			tool: func(s *Server) agent.Tool { return &outboundPolicyTool{server: s} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				return map[string]any{"resetDefault": true}, nil
			}},
		// ---- session ----
		&cliCommand{group: "session", name: "title", summary: "把会话标题改成任务概括（≤16 字动宾短语）",
			usage:       "elysia session title <文本>",
			example:     `elysia session title 接入Anthropic协议`,
			positionals: []cliPositionalSpec{{"title", "新标题"}},
			tool:        func(s *Server) agent.Tool { return &updateTitleTool{} },
			mapper: func(inv *cliInvocation) (map[string]any, error) {
				m := map[string]any{}
				if len(inv.args) == 0 || strings.TrimSpace(strings.Join(inv.args, " ")) == "" {
					return nil, fmt.Errorf("缺少位置参数 <title>")
				}
				m["title"] = strings.Join(inv.args, " ")
				return m, nil
			}},
	)
	return table
}

// cliWindowFlags / cliWindowMapper 是用量类命令共享的时间窗与过滤参数。
func cliWindowFlags() []cliFlagSpec {
	return []cliFlagSpec{
		{"days", "最近 N 天（默认 7，最大 366）", false, false},
		{"from", "起始时间（RFC3339）", false, false},
		{"to", "结束时间（RFC3339）", false, false},
		{"model", "按模型名过滤", false, false},
		{"key", "按 API Key 名过滤", false, false},
		{"group", "按模型组过滤", false, false},
	}
}

func cliWindowMapper(inv *cliInvocation) (map[string]any, error) {
	m := map[string]any{}
	if _, err := inv.setInt(m, "days", "days"); err != nil {
		return nil, err
	}
	inv.setStr(m, "from", "from")
	inv.setStr(m, "to", "to")
	inv.setStr(m, "model", "modelName")
	inv.setStr(m, "key", "keyName")
	inv.setStr(m, "group", "groupName")
	return m, nil
}

func cliMissing(flag string) error {
	return fmt.Errorf("缺少必填参数 --%s", flag)
}

// cliResolve 在命令表里解析调用（flag 规格驱动解析）。
func cliResolve(args []string) (*cliInvocation, error) {
	table := cliCommandTable()
	// 先按最长前缀匹配命令路径（组 [命令 [二级]]）。
	var matched *cliCommand
	var rest []string
	for depth := 3; depth >= 1; depth-- {
		if len(args) < depth {
			continue
		}
		path := strings.Join(args[:depth], " ")
		for _, command := range table {
			if command.Path() == path {
				matched = command
				rest = args[depth:]
				break
			}
		}
		if matched != nil {
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("未知命令 %q（运行 elysia help 查看全部命令）", strings.Join(args, " "))
	}
	inv := &cliInvocation{command: matched, flags: map[string]string{}, present: map[string]bool{}}
	for i := 0; i < len(rest); i++ {
		token := rest[i]
		if token == "--" {
			inv.args = append(inv.args, rest[i+1:]...)
			break
		}
		if !strings.HasPrefix(token, "--") {
			inv.args = append(inv.args, token)
			continue
		}
		name := strings.TrimPrefix(token, "--")
		value := ""
		hasInline := false
		if at := strings.Index(name, "="); at >= 0 {
			value = name[at+1:]
			name = name[:at]
			hasInline = true
		}
		spec := matched.flagByName(name)
		if spec == nil {
			return nil, fmt.Errorf("命令 %s 没有参数 --%s（运行 elysia help %s 查看）", matched.Path(), name, matched.Path())
		}
		if !hasInline {
			if spec.boolean {
				value = "true"
			} else if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") {
				value = rest[i+1]
				i++
			} else {
				return nil, fmt.Errorf("--%s 需要一个值（--%s <值>）", name, name)
			}
		}
		if spec.list {
			if existing, ok := inv.flags[name]; ok {
				value = existing + "," + value
			}
		}
		inv.flags[name] = value
		inv.present[name] = true
	}
	if extra := len(inv.args) - len(matched.positionals); extra > 0 {
		return nil, fmt.Errorf("命令 %s 最多接受 %d 个位置参数", matched.Path(), len(matched.positionals))
	}
	return inv, nil
}

// ---- 门控探针 ----

// cliGateNote 是探针产出的待批描述：哪条命令需要哪个权限键。
type cliGateNote struct {
	Command string
	Key     string
}

// probeAgentCLI 解析整批脚本，收集所有需要审批的命令（不执行）。
// 语句级解析失败不中断收集：已识别的门控命令必须照常上报，否则一条坏
// 语句会挟带同批的门控命令绕过审批（`;` 批在执行侧仍会跑后续语句）。
// 整批切分失败（如引号未闭合）返回 error——执行阶段同样切不开，调用方
// 按放行处理即可。Command 一律打码：它会进审批卡说明等出站出口。
func probeAgentCLI(script string) ([]cliGateNote, error) {
	segments, err := cliSplitStatements(script)
	if err != nil {
		return nil, err
	}
	notes := []cliGateNote{}
	var parseErr error
	for _, segment := range segments {
		statement, err := cliParseStatement(segment.raw)
		if err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		if len(statement.args) == 0 || statement.args[0] == "help" || statement.args[0] == "--help" {
			continue
		}
		inv, err := cliResolve(statement.args)
		if err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		// 权限与目标工具同源：Gated/PermissionKey 不触碰 server，nil 实例安全。
		tool := inv.command.tool(nil)
		if tool.Gated() {
			notes = append(notes, cliGateNote{Command: "$ " + agent.RedactCommandLine(segment.raw), Key: tool.PermissionKey()})
		}
	}
	return notes, parseErr
}

// ---- 执行 ----

// runAgentCLI 解析并执行一段 elysia 脚本，返回模型可读的结果。
func (s *Server) runAgentCLI(ctx context.Context, tctx agent.ToolContext, script string) agent.ToolResult {
	segments, err := cliSplitStatements(script)
	if err != nil {
		return agent.ToolError("脚本解析失败: "+err.Error(), "parse_failed")
	}
	var output strings.Builder
	succeeded, failed := 0, 0
	for _, segment := range segments {
		cmdOutput, ok := s.runOneCLIStatement(ctx, tctx, segment.raw)
		output.WriteString(cmdOutput)
		if ok {
			succeeded++
		} else {
			failed++
			// `&&` 分隔的批在首个失败后停止（`;` 继续执行余下命令）。
			if segment.mustSucceed {
				break
			}
		}
	}
	summary := fmt.Sprintf("执行 %d 条命令：%d 成功、%d 失败", succeeded+failed, succeeded, failed)
	text := clampCLIOutput(output.String())
	return agent.ToolResult{OK: failed == 0, Summary: summary,
		Data: map[string]any{"output": text, "exitCode": failed == 0}}
}

// runOneCLIStatement 执行单条语句，返回渲染后的文本块与成败。
func (s *Server) runOneCLIStatement(ctx context.Context, tctx agent.ToolContext, raw string) (string, bool) {
	var block strings.Builder
	// 回显打码：输出会随 tool_result 落库并回放给模型，敏感 flag 的值
	// 不允许经此二次出站（模型自己发的命令，原文在它的上下文里）。
	block.WriteString("$ " + agent.RedactCommandLine(raw) + "\n")
	statement, err := cliParseStatement(raw)
	if err != nil {
		block.WriteString("错误: " + err.Error() + "\n\n")
		return block.String(), false
	}
	if len(statement.args) == 0 || statement.args[0] == "help" || statement.args[0] == "--help" {
		block.WriteString(renderCLIHelp(statement.args[1:]) + "\n\n")
		return block.String(), true
	}
	inv, err := cliResolve(statement.args)
	if err != nil {
		block.WriteString("错误: " + err.Error() + "\n\n")
		return block.String(), false
	}
	args, err := inv.command.mapper(inv)
	if err != nil {
		block.WriteString("错误: " + err.Error() + "\n\n")
		return block.String(), false
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		block.WriteString("错误: 参数序列化失败: " + err.Error() + "\n\n")
		return block.String(), false
	}
	tool := inv.command.tool(s)
	toolTimeout := agent.MetaOf(tool).TimeoutMs
	if toolTimeout <= 0 {
		toolTimeout = 120_000
	}
	execCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Duration(toolTimeout)*time.Millisecond {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(toolTimeout)*time.Millisecond)
		defer cancel()
	}
	result := tool.Execute(execCtx, tctx, encoded)
	text := renderCLIResult(result, statement)
	block.WriteString(text + "\n\n")
	return block.String(), result.OK
}

// renderCLIResult 把单条命令的 ToolResult 渲染为文本（含管道过滤）。
func renderCLIResult(result agent.ToolResult, statement cliStatement) string {
	var text string
	if result.OK {
		text = result.Summary
	} else {
		text = "失败: " + result.Summary
	}
	if result.Data != nil {
		if encoded, err := json.Marshal(result.Data); err == nil {
			text += "\n" + string(encoded)
		}
	}
	if statement.hasGrep {
		text = filterLines(text, statement.grep)
	}
	if statement.head > 0 {
		text = headLines(text, statement.head)
	}
	return text
}

func filterLines(text, pattern string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), strings.ToLower(pattern)) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func headLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n") + "\n…（已截断）"
}

// clampCLIOutput 把总输出压进预算：保头部 24KB + 截断标记 + 尾部 4KB。
func clampCLIOutput(text string) string {
	if len(text) <= cliOutputBudgetBytes {
		return text
	}
	head := cliOutputBudgetBytes - 8*1024
	tail := 4 * 1024
	return text[:head] + "\n…[输出超限，中间已截断]…\n" + text[len(text)-tail:]
}
