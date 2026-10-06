package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// 显式设置 ELYSIA_AGENT_EVAL_MODEL_FILE 才调用真实模型（会产生模型用量）。
// 文件为 {"source": storage.ModelSource, "model": storage.Model}；凭证只写入
// t.TempDir 的临时库。所有运维数据为本测试的合成数据，不连接真实业务库。
func TestCLILivePromptTasks(t *testing.T) {
	path := os.Getenv("ELYSIA_AGENT_EVAL_MODEL_FILE")
	if path == "" {
		t.Skip("set ELYSIA_AGENT_EVAL_MODEL_FILE to run real-model prompt tasks")
	}
	var fixture struct {
		Source storage.ModelSource
		Model  storage.Model
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ name, request string }{
		{"failed_logs", "查一下最近一天的失败调用日志，给我最近两条的请求 ID、状态码和错误原因。"},
		{"create_group", "请用测试源里的模型创建模型组 eval-group，按模型顺序失败回退，不需要创建 API Key。"},
		{"empty_models", "空源的模型列表是空的，帮我检查原因并解释目前能确认什么。"},
		{"edit_draft", "把当前协议草稿的请求路径改为 /v2/chat/completions，其他配置保留；只改草稿，不保存正式协议，不出站。"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := newAgentIntegrationServer(t)
			s.protocolTransport = relay.NewProtocolTransport(120 * time.Second)
			ctx := t.Context()
			if err := s.store.UpsertSource(ctx, fixture.Source); err != nil {
				t.Fatal(err)
			}
			if err := s.store.ReplaceSourceModels(ctx, fixture.Source, []storage.Model{fixture.Model}); err != nil {
				t.Fatal(err)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[]}`))
			}))
			t.Cleanup(upstream.Close)
			// 仅在隔离测试中让模型发现请求访问本地合成上游。
			relay.SetAllowPrivateDial(true)

			models := []storage.Model{{ID: "eval-a", Name: "eval-a", BaseURL: upstream.URL, Type: "llm", Enabled: true}, {ID: "eval-b", Name: "eval-b", BaseURL: upstream.URL, Type: "llm", Enabled: true}}
			source := storage.ModelSource{ID: "catalog", Name: "测试源", BaseURL: upstream.URL, Platform: "openai", Enabled: true, ManualModels: models}
			if err := s.store.UpsertSource(ctx, source); err != nil {
				t.Fatal(err)
			}
			if err := s.store.ReplaceSourceModels(ctx, source, models); err != nil {
				t.Fatal(err)
			}
			if err := s.store.UpsertSource(ctx, storage.ModelSource{ID: "empty", Name: "空源", BaseURL: upstream.URL, Platform: "openai", Enabled: true, AutoFetchModels: true}); err != nil {
				t.Fatal(err)
			}
			for i, code := range []int{429, 502, 200} {
				record := storage.UsageLogItem{RequestID: fmt.Sprintf("eval-request-%d", i+1), StartedAt: time.Now().Add(-time.Duration(i+1) * time.Minute), ModelName: "eval-a", SourceID: "catalog", StatusCode: code, Error: map[int]string{429: "rate limit", 502: "upstream unavailable"}[code]}
				payload, _ := json.Marshal(record)
				if err := s.store.SaveUsageRecordJSON(ctx, payload, record, record.StartedAt.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			input := storage.AgentSessionUpsert{Mode: agent.ModeCreate, Settings: agent.Settings{ModelSourceID: fixture.Source.ID, ModelName: fixture.Model.Name, AllowSave: agent.PermissionAsk, AllowLiveTest: agent.PermissionAsk, AllowDelete: agent.PermissionNever}}
			if scenario.name == "edit_draft" {
				input.Mode = agent.ModeEdit
				input.ProtocolID = "eval-protocol"
				input.SeedConfig = `{"id":"eval-protocol","name":"测试协议","request":{"method":"POST","path":"/v1/chat/completions","body":{"model":{"field":"model","mode":"string"}}},"response":{"fields":[{"path":"choices[0].message.content","field":"text"}]}}`
			}
			session, err := s.store.CreateAgentSession(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			engine := s.protocolAgentEngine()
			t.Cleanup(func() { engine.Stop(session.ID) })
			events, err := engine.RunTurn(ctx, session.ID, &agent.UserContent{Text: scenario.request})
			if err != nil {
				t.Fatal(err)
			}
			commands := []string{}
			final := ""
			observedCatalog := false
			for approvals := 0; ; approvals++ {
				for event := range events {
					if event.Type == agent.EventToolCall {
						if event.Name != "elysia_cli" && event.Name != "ask_user" && event.Name != "update_plan" {
							t.Errorf("unexpected tool %s", event.Name)
						}
						if event.Name == "elysia_cli" {
							params, err := parseCLIToolParams(event.Input)
							if err != nil {
								t.Fatal(err)
							}
							commands = append(commands, params.Command)
							t.Logf("command: %s", params.Command)
						}
					}
					if event.Message != nil && event.Message.Role == agent.RoleAssistant {
						var content agent.AssistantContent
						_ = json.Unmarshal(event.Message.Content, &content)
						if content.Text != "" {
							final = content.Text
						}
						for _, call := range content.ToolCalls {
							params, _ := parseCLIToolParams(call.Arguments)
							if scenario.name == "create_group" && call.Name == "elysia_cli" && strings.Contains(params.Command, "elysia group create") && !observedCatalog {
								t.Error("generated group mutation before observing catalog")
							}
						}
					}
					if event.Result != nil {
						if strings.Contains(string(event.Result.Data), "eval-a") && strings.Contains(string(event.Result.Data), "eval-b") {
							observedCatalog = true
						}
						t.Logf("result: ok=%t %s", event.Result.OK, event.Result.Summary)
						if !event.Result.OK {
							t.Logf("failure detail: %s", event.Result.Data)
						}
					}
					if event.Type == agent.EventError {
						t.Errorf("model turn failed: %s", event.Text)
					}
				}
				session, err = s.store.GetSession(ctx, session.ID)
				if err != nil {
					t.Fatal(err)
				}
				if session.Status != agent.StatusWaitingApproval {
					break
				}
				if approvals >= 3 || session.PendingAction == nil || session.PendingAction.Kind != agent.PendingKindApproval {
					t.Fatalf("unexpected pending interaction: %s", session.Status)
				}
				// 只批准测试要求的合成数据写入/本地空源刷新。
				for _, call := range session.PendingAction.Calls {
					params, err := parseCLIToolParams(call.Arguments)
					if err != nil {
						t.Fatal(err)
					}
					if call.Name != "elysia_cli" || !allowCLIEvalApproval(scenario.name, params.Command) {
						t.Fatalf("unexpected approval command: %s", params.Command)
					}
				}
				events, err = engine.ResumeApproval(ctx, session.ID, agent.ApprovalDecision{Approved: true})
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("final: %s", final)
			if len(commands) == 0 || final == "" {
				t.Fatal("task did not query tools or report a result")
			}
			switch scenario.name {
			case "failed_logs":
				if !strings.Contains(final, "eval-request-1") || !strings.Contains(final, "eval-request-2") || strings.Contains(final, "eval-request-3") {
					t.Error("failed log report does not match fixture")
				}
			case "create_group":
				groups, _ := s.store.ListGroups(ctx)
				if len(groups) != 1 || groups[0].Name != "eval-group" || groups[0].Strategy != "sequential" || len(groups[0].Models) == 0 {
					t.Error("group does not match requested result")
				}
			case "empty_models":
				for _, claim := range []string{"说明没有刷新过", "肯定没刷新", "从未刷新过"} {
					if strings.Contains(final, claim) {
						t.Errorf("unsupported empty-catalog claim: %s", claim)
					}
				}
			case "edit_draft":
				// 服务端会按 CustomProtocolConfig 重新序列化草稿，比较规范化
				// 后的配置，避免把默认 auth:{} 等零值字段误报为模型改动。
				var before, after relay.CustomProtocolConfig
				_ = json.Unmarshal([]byte(input.SeedConfig), &before)
				_ = json.Unmarshal(session.DraftConfig, &after)
				if after.Request.PathTemplate != "/v2/chat/completions" {
					t.Fatal("draft path not updated")
				}
				before.Request.PathTemplate = "/v2/chat/completions"
				wantJSON, _ := json.Marshal(before)
				gotJSON, _ := json.Marshal(after)
				var want, got any
				_ = json.Unmarshal(wantJSON, &want)
				_ = json.Unmarshal(gotJSON, &got)
				if !reflect.DeepEqual(want, got) {
					t.Errorf("unrequested draft changes: %s", gotJSON)
				}
			}
		})
	}
}

// 不因某个批次包含获准命令就批准整个批次；检查所有会产生副作用的命令。
func allowCLIEvalApproval(scenario, script string) bool {
	segments, err := cliSplitStatements(script)
	if err != nil {
		return false
	}
	for _, segment := range segments {
		statement, err := cliParseStatement(segment.raw)
		if err != nil {
			// 与 ProbeGates 一致：坏语句在执行阶段只会报错，不能产生副作用。
			continue
		}
		if isCLIHelpArgs(statement.args) {
			continue
		}
		inv, err := cliResolve(statement.args)
		if err != nil {
			continue
		}
		if cliEffectOf(inv.command.handler(nil)) == CLIEffectRead {
			continue
		}
		if scenario == "create_group" && inv.command.Path() == "group create" && inv.flags["name"] == "eval-group" {
			continue
		}
		if scenario == "empty_models" && inv.command.Path() == "source refresh" && (inv.flags["source"] == "empty" || inv.flags["source"] == "空源") {
			continue
		}
		return false
	}
	return true
}
