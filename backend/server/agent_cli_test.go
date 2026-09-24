package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/storage"
)

// elysia CLI 层测试：解析器、参数映射、门控探针、脱敏、help 与等价执行。

func TestCLIParser(t *testing.T) {
	// 引号与 flag=value。
	tokens, err := cliTokenize(`protocol draft '{"id":"x"}' --example='{"a":1}'`)
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	want := []string{"protocol", "draft", `{"id":"x"}`, "--example=" + `{"a":1}`}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %v", tokens)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Fatalf("token[%d] = %q want %q", i, tokens[i], want[i])
		}
	}

	// 批处理切分：&& 与 ; 的语义标记（&& 右侧段标 fromAnd）。
	segments, err := cliSplitStatements("elysia source ls && elysia group ls;\nelysia key ls")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(segments) != 3 || !segments[0].mustSucceed || segments[1].mustSucceed || segments[2].mustSucceed ||
		segments[0].fromAnd || !segments[1].fromAnd || segments[2].fromAnd {
		t.Fatalf("segments = %+v", segments)
	}

	// 管道（按书写顺序）与 head 的三种习惯写法。
	statement, err := cliParseStatement("elysia usage logs --status failed | grep timeout | head 5")
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if len(statement.pipes) != 2 || statement.pipes[0].kind != pipeGrep || statement.pipes[0].text != "timeout" ||
		statement.pipes[1].kind != pipeHead || statement.pipes[1].n != 5 {
		t.Fatalf("statement pipes = %+v", statement.pipes)
	}
	for _, form := range []string{"elysia source ls | head -n 7", "elysia source ls | head -7"} {
		statement, err = cliParseStatement(form)
		if err != nil || statement.pipes[0].n != 7 {
			t.Fatalf("head form %q: err=%v pipes=%+v", form, err, statement.pipes)
		}
	}
	// 管道顺序按书写执行：先 head 后 grep 时，第 n 行之外的匹配行不保留。
	statement, _ = cliParseStatement("elysia source ls | head 1 | grep zz")
	if len(statement.pipes) != 2 || statement.pipes[0].kind != pipeHead {
		t.Fatalf("pipes must follow written order: %+v", statement.pipes)
	}
	// 不支持的管道。
	if _, err := cliParseStatement("elysia source ls | awk '{print $1}'"); err == nil {
		t.Fatalf("unsupported pipe must fail")
	}
	// 空引号 token 与空位置参数报错。
	tokens, err = cliTokenize(`--secret '' keep`)
	if err != nil || len(tokens) != 3 || tokens[1] != "" {
		t.Fatalf("empty-quoted token: %v err=%v", tokens, err)
	}
	if _, err := cliResolve(mustArgs(t, "elysia session title ''")); err == nil {
		t.Fatalf("empty positional must fail")
	}
	// 双引号内 \" 不改变引号态：; 与 | 不被误切。
	segments, err = cliSplitStatements(`elysia session title "a\"; elysia session title b" ; elysia session title c`)
	if err != nil {
		t.Fatalf("escaped quote split: %v", err)
	}
	if len(segments) != 2 || !strings.HasSuffix(segments[0].raw, `session title b"`) {
		t.Fatalf("segments = %+v", segments)
	}
	if _, err := cliParseStatement(`elysia source ls --name "x\"| y"`); err != nil {
		t.Fatalf("escaped quote must survive pipe split: %v", err)
	}
	// 裸 & 明确报错。
	if _, err := cliSplitStatements("elysia source ls & elysia group ls"); err == nil {
		t.Fatalf("single & must fail")
	}
}

func TestCLIResolveMappers(t *testing.T) {
	resolve := func(line string) *cliInvocation {
		t.Helper()
		statement, err := cliParseStatement(line)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		inv, err := cliResolve(statement.args)
		if err != nil {
			t.Fatalf("resolve %q: %v", line, err)
		}
		return inv
	}
	encode := func(inv *cliInvocation) map[string]any {
		t.Helper()
		m, err := inv.command.mapper(inv)
		if err != nil {
			t.Fatalf("map %s: %v", inv.command.Path(), err)
		}
		return m
	}

	// 布尔 false / 列表合并 / 缺省保留。
	m := encode(resolve(`elysia key update --name k1 --enabled=false --allowed-groups a,b --new-secret sk-2`))
	if m["enabled"] != false || m["newSecret"] != "sk-2" {
		t.Fatalf("key update args = %v", m)
	}
	if list, ok := m["allowedGroups"].([]string); !ok || len(list) != 2 || list[0] != "a" {
		t.Fatalf("allowedGroups = %v", m["allowedGroups"])
	}
	m = encode(resolve(`elysia source update --source s1 --enabled`))
	if m["enabled"] != true || m["apiKey"] != nil {
		t.Fatalf("source update args = %v（未传字段不得出现）", m)
	}

	// 重复列表 flag 与整数。
	m = encode(resolve(`elysia group create --name g --models a --models b,c --max-retries 5`))
	if list, ok := m["models"].([]string); !ok || len(list) != 3 {
		t.Fatalf("models = %v", m["models"])
	}
	if m["maxRetries"] != 5 {
		t.Fatalf("maxRetries = %v", m)
	}

	// 位置参数 JSON（protocol draft）。
	m = encode(resolve(`elysia protocol draft '{"id":"p1"}' --example '{"text":"hi"}'`))
	if string(m["config"].(json.RawMessage)) != `{"id":"p1"}` {
		t.Fatalf("config = %v", m["config"])
	}
	if string(m["exampleResponse"].(json.RawMessage)) != `{"text":"hi"}` {
		t.Fatalf("example = %v", m["exampleResponse"])
	}

	// usage log 位置参数与二级命令。
	m = encode(resolve(`elysia usage log req-9`))
	if m["requestId"] != "req-9" {
		t.Fatalf("requestId = %v", m)
	}
	m = encode(resolve(`elysia group member add --group g --models s1:m1`))
	if list, ok := m["addModels"].([]string); !ok || list[0] != "s1:m1" {
		t.Fatalf("addModels = %v", m["addModels"])
	}

	// outbound set 空列表表达「全放行」（[] 而非 null——null 是工具端的只读分支）。
	m = encode(resolve(`elysia outbound set --ranges=`))
	if list, ok := m["ranges"].([]string); !ok || len(list) != 0 {
		t.Fatalf("empty ranges must encode as []: %#v", m["ranges"])
	}
	m = encode(resolve(`elysia outbound set --ranges 10.0.0.0/8,172.16.0.0/12`))
	if list, ok := m["ranges"].([]string); !ok || len(list) != 2 {
		t.Fatalf("ranges = %v", m["ranges"])
	}

	// 组级命令（syslog）可达。
	inv := resolve(`elysia syslog --level error --limit 5`)
	if inv.command.Path() != "syslog" {
		t.Fatalf("syslog path = %q", inv.command.Path())
	}

	// 必填缺失（mapper 层校验）与未知 flag（resolver 层）报错。
	if inv, err := cliResolve(mustArgs(t, "elysia source create --name x")); err == nil {
		if _, mapErr := inv.command.mapper(inv); mapErr == nil {
			t.Fatalf("missing base-url must fail")
		}
	}
	if _, err := cliResolve(mustArgs(t, "elysia source ls --bogus")); err == nil {
		t.Fatalf("unknown flag must fail")
	}
}

func mustArgs(t *testing.T, line string) []string {
	t.Helper()
	statement, err := cliParseStatement(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return statement.args
}

// 全部命令：路径必须可被自身组词 resolve（防「syslog 尾随空格」类不可
// 达回归），带最小合法参数时 mapper 必须成功（防 flag 键名拼写回归）。
func TestCLIAllCommandsResolve(t *testing.T) {
	// 路径词数不得超出 lookupCLICommand 的搜索深度，否则静默不可达。
	for _, command := range cliCommandTable() {
		if words := len(strings.Fields(command.Path())); words > cliMaxCommandWords {
			t.Fatalf("path %q has %d words, exceeds cliMaxCommandWords=%d", command.Path(), words, cliMaxCommandWords)
		}
	}
	for _, command := range cliCommandTable() {
		path := command.Path()
		statement, err := cliParseStatement("elysia " + path)
		if err != nil {
			t.Fatalf("path %q: parse: %v", path, err)
		}
		inv, err := cliResolve(statement.args)
		if err != nil {
			t.Fatalf("path %q does not resolve: %v", path, err)
		}
		if inv.command.Path() != path {
			t.Fatalf("path %q resolved to %q", path, inv.command.Path())
		}
	}
	// 最小参数 mapper 冒烟（覆盖其余测试没碰到的命令）。
	for _, line := range []string{
		`elysia source refresh --source s1`,
		`elysia model ls --source s1 --search gpt --limit 20`,
		`elysia model set --source s1 --model m1 --vision --max-tokens 4096 --enabled=false`,
		`elysia model rm --source s1 --model m1`,
		`elysia group update --group g1 --add-models s1:m1 --remove-models s1:m2 --strategy random --daily-limit-requests 10`,
		`elysia group delete --group g1`,
		`elysia group member rm --group g1 --models s1:m1`,
		`elysia key ls`,
		`elysia key create --name k1 --allowed-groups g1`,
		`elysia key delete --name k1`,
		`elysia protocol preview --sample '{"model":"m"}'`,
		`elysia protocol test --base-url https://u.io --api-key sk --stream`,
		`elysia protocol models --base-url https://u.io`,
		`elysia protocol save`,
		`elysia protocol read --id anthropic-api`,
		`elysia usage stats --days 7 --group g1`,
		`elysia usage trend --days 30`,
		`elysia usage logs --days 1 --status failed --limit 5`,
		`elysia outbound get`,
		`elysia outbound reset`,
		`elysia syslog --level error --limit 5`,
	} {
		statement, err := cliParseStatement(line)
		if err != nil {
			t.Fatalf("%s: parse: %v", line, err)
		}
		inv, err := cliResolve(statement.args)
		if err != nil {
			t.Fatalf("%s: resolve: %v", line, err)
		}
		if _, err := inv.command.mapper(inv); err != nil {
			t.Fatalf("%s: mapper: %v", line, err)
		}
	}
}

func TestCLIProbeGates(t *testing.T) {
	notes, err := probeAgentCLI("elysia source ls && elysia source create --name a --base-url https://x.io ; elysia key delete --name k")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(notes) != 2 || notes[0].PermissionKey != "save" || notes[1].PermissionKey != "delete" {
		t.Fatalf("notes = %+v", notes)
	}
	// help 与只读命令不产生门控。
	notes, err = probeAgentCLI("elysia help source\nelysia usage stats --days 1")
	if err != nil || len(notes) != 0 {
		t.Fatalf("read-only notes = %+v err=%v", notes, err)
	}
	// outbound get 是只读查询，不门控；set/reset 仍按 save 门控。
	notes, err = probeAgentCLI("elysia outbound get")
	if err != nil || len(notes) != 0 {
		t.Fatalf("outbound get must not gate: %+v err=%v", notes, err)
	}
	notes, err = probeAgentCLI("elysia outbound set --ranges 10.0.0.0/8 ; elysia outbound reset")
	if err != nil || len(notes) != 2 || notes[0].PermissionKey != "save" || notes[1].PermissionKey != "save" {
		t.Fatalf("outbound set/reset must gate: %+v err=%v", notes, err)
	}
	// 模型回传预算必须覆盖 CLI 输出预算（否则落进引擎 16KB preview 信封）。
	if meta := agent.MetaOf(&bashTool{}); meta.MaxModelBytes < cliOutputBudgetBytes {
		t.Fatalf("bash model budget %d must cover output budget %d", meta.MaxModelBytes, cliOutputBudgetBytes)
	}
	// 解析失败：ok=false 语义由调用方兜底。
	if _, err := probeAgentCLI("elysia source ls --bogus"); err == nil {
		t.Fatalf("parse error must surface")
	}
}

// 坏语句不得挟带同批门控命令绕过审批：语句级解析失败后继续收集，
// ProbeGates 只要识别出门控命令就必须走聚合判定。
func TestCLIProbePartialFailureKeepsGates(t *testing.T) {
	notes, err := probeAgentCLI("elysia source ls --bogus ; elysia key delete --name prod")
	if err == nil {
		t.Fatalf("parse error must surface alongside notes")
	}
	if len(notes) != 1 || notes[0].PermissionKey != "delete" {
		t.Fatalf("gated command must survive partial parse failure: %+v", notes)
	}

	s := newAgentIntegrationServer(t)
	bash := &bashTool{server: s}
	gates, ok := bash.ProbeGates(json.RawMessage(`{"command":"elysia source ls --bogus\nelysia source delete --source s1"}`))
	if !ok || len(gates) != 1 || gates[0].PermissionKey != "delete" {
		t.Fatalf("probe must gate despite partial parse failure: ok=%v gates=%+v", ok, gates)
	}
	// 完全没有可识别门控命令且解析失败：ok=false（执行阶段报可读错误）。
	if gates, ok := bash.ProbeGates(json.RawMessage(`{"command":"elysia source ls --bogus"}`)); ok || len(gates) != 0 {
		t.Fatalf("clean parse failure must defer to execution: ok=%v gates=%+v", ok, gates)
	}
}

// 探针上报的命令文本必须打码：它会进审批卡说明等出站出口。
func TestCLIProbeNotesMasked(t *testing.T) {
	notes, err := probeAgentCLI(`elysia source create --name x --base-url https://u.io --api-key sk-live-123`)
	if err != nil || len(notes) != 1 {
		t.Fatalf("probe: notes=%+v err=%v", notes, err)
	}
	if strings.Contains(notes[0].Command, "sk-live-123") {
		t.Fatalf("note leaked secret: %s", notes[0].Command)
	}
	if !strings.Contains(notes[0].Command, "--api-key ***") {
		t.Fatalf("note missing mask: %s", notes[0].Command)
	}
}

func TestCLIBashCommandMasking(t *testing.T) {
	raw := json.RawMessage(`{"command":"elysia protocol test --api-key sk-secret-1 --stream"}`)
	masked := agent.MaskSecretInputs(raw)
	if strings.Contains(string(masked), "sk-secret-1") {
		t.Fatalf("secret leaked: %s", masked)
	}
	if !strings.Contains(string(masked), "--stream") || !strings.Contains(string(masked), "***") {
		t.Fatalf("masking broke the command: %s", masked)
	}
	// --key 是用量过滤的 Key 名，不打码。
	raw = json.RawMessage(`{"command":"elysia usage logs --key mobile-app"}`)
	masked = agent.MaskSecretInputs(raw)
	if !strings.Contains(string(masked), "mobile-app") {
		t.Fatalf("non-secret flag over-masked: %s", masked)
	}
}

func TestCLIHelp(t *testing.T) {
	overview := renderCLIHelp(nil)
	for _, group := range []string{"source", "model", "group", "key", "protocol", "usage", "outbound", "session"} {
		if !strings.Contains(overview, group) {
			t.Fatalf("overview missing group %s", group)
		}
	}
	group := renderCLIHelp([]string{"source"})
	if !strings.Contains(group, "--base-url") || !strings.Contains(group, "refresh") {
		t.Fatalf("group help incomplete: %s", group)
	}
	command := renderCLIHelp([]string{"protocol", "test"})
	if !strings.Contains(command, "--api-key") || !strings.Contains(command, "审批门控") || !strings.Contains(command, "示例") {
		t.Fatalf("command help incomplete: %s", command)
	}
	if !strings.Contains(renderCLIHelp([]string{"bogus"}), "没有命令组") {
		t.Fatalf("unknown group hint missing")
	}
}

// 等价执行：CLI 命令经真实 Server 落库，结果与既有工具行为一致。
func TestCLIRunEquivalence(t *testing.T) {
	s := newAgentIntegrationServer(t)
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	tctx := &opsTestContext{}

	// 批处理：创建源 + 建组（组员引用模型）+ 查询过滤。
	result := s.runAgentCLI(ctx, tctx,
		"elysia source create --name src1 --base-url "+upstream.URL+" --manual-models m-a ; elysia group create --name g1 --models src1:m-a ; elysia group ls | grep g1")
	if !result.OK {
		t.Fatalf("batch failed: %s", result.Summary)
	}
	output := cliOutputText(t, result)
	if !strings.Contains(output, "$ elysia group create") || !strings.Contains(output, "g1") {
		t.Fatalf("batch output wrong:\n%s", output)
	}
	sources, _ := s.store.ListSources(ctx)
	if len(sources) != 1 || sources[0].ID != "src1" {
		t.Fatalf("source not created: %+v", sources)
	}
	groups, _ := s.store.ListGroups(ctx)
	if len(groups) != 1 || len(groups[0].Models) != 1 {
		t.Fatalf("group not created: %+v", groups)
	}

	// && 失败即停：失败命令之后的链上命令不执行（失败命令自身仍会回显错误）。
	result = s.runAgentCLI(ctx, tctx, "elysia source delete && elysia source ls")
	if result.OK {
		t.Fatalf("batch with error must fail")
	}
	if strings.Contains(cliOutputText(t, result), "$ elysia source ls") {
		t.Fatalf("&& must stop after failure")
	}

	// && 链跳过但 ; 边界恢复：`a && b; c` 中 a 失败时 c 照常执行。
	created2, _ := s.store.CreateAgentSession(ctx, storage.AgentSessionUpsert{Mode: "create"})
	session2, _ := s.store.GetSession(ctx, created2.ID)
	realTctx2 := &sessionToolContext{ctx: ctx, store: s.store, session: session2}
	result = s.runAgentCLI(ctx, realTctx2, "elysia source delete && elysia source ls ; elysia session title 链恢复")
	if result.OK {
		t.Fatalf("batch with failed && must fail overall")
	}
	chainOut := cliOutputText(t, result)
	if strings.Contains(chainOut, "$ elysia source ls") || !strings.Contains(chainOut, "$ elysia session title") {
		t.Fatalf("&& chain skip / ; resume wrong:\n%s", chainOut)
	}
	if session2.Title != "链恢复" {
		t.Fatalf("title after chain skip = %q", session2.Title)
	}

	// 组级命令 syslog 真实执行；exitCode 为整数 0。
	result = s.runAgentCLI(ctx, tctx, "elysia syslog --limit 5")
	if !result.OK {
		t.Fatalf("syslog failed: %s", result.Summary)
	}
	if data, ok := result.Data.(map[string]any); !ok || data["exitCode"] != 0 {
		t.Fatalf("syslog exitCode = %#v", result.Data)
	}

	// help 输出支持 grep 管道。
	result = s.runAgentCLI(ctx, tctx, "elysia help | grep syslog")
	if !result.OK {
		t.Fatalf("help pipe failed: %s", result.Summary)
	}
	helpOut := cliOutputText(t, result)
	if !strings.Contains(helpOut, "系统日志") || strings.Contains(helpOut, "模型源管理") {
		t.Fatalf("help pipe output wrong:\n%s", helpOut)
	}

	// 回显打码：输出随 tool_result 落库回放，敏感 flag 值不得明文出现。
	result = s.runAgentCLI(ctx, tctx, "elysia source create --name src2 --base-url "+upstream.URL+" --manual-models m-b --api-key sk-live-456")
	if !result.OK {
		t.Fatalf("masked echo batch failed: %s", result.Summary)
	}
	maskedOutput := cliOutputText(t, result)
	if strings.Contains(maskedOutput, "sk-live-456") {
		t.Fatalf("echo leaked secret:\n%s", maskedOutput)
	}
	if !strings.Contains(maskedOutput, "--api-key ***") {
		t.Fatalf("echo missing mask:\n%s", maskedOutput)
	}

	// session title 委托真实会话上下文。
	created, _ := s.store.CreateAgentSession(ctx, storage.AgentSessionUpsert{Mode: "create"})
	session, _ := s.store.GetSession(ctx, created.ID)
	realTctx := &sessionToolContext{ctx: ctx, store: s.store, session: session}
	result = s.runAgentCLI(ctx, realTctx, "elysia session title 接入测试协议")
	if !result.OK {
		t.Fatalf("title failed: %s", result.Summary)
	}
	if session.Title != "接入测试协议" {
		t.Fatalf("title = %q", session.Title)
	}
}

func cliOutputText(t *testing.T, result agent.ToolResult) string {
	t.Helper()
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("result data = %#v", result.Data)
	}
	text, _ := data["output"].(string)
	return text
}
