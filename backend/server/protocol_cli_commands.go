package server

import "fmt"

var protocolFlagUsage = map[string]string{
	"sample":    "完整输入 JSON；真实生成探测使用语义请求",
	"direction": "映射方向，取值以 protocol schema 为准",
	"sequence":  "将输入作为有序事件序列验证",
	"mode":      "mapping、session、task、models 或 agent",
	"sample-id": "定义中已声明的会话样例 ID",
	"operation": "定义中已声明的操作名",
	"kind":      "任务映射方向：decode、encode 或 control",
	"purpose":   "任务阶段：submit、status、result 或 cancel",
	"base-url":  "真实上游地址；仍受出站地址策略限制",
	"api-key":   "真实上游凭据；不写入验证报告",
	"expected":  "保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改",
	"id":        "协议 ID，区分大小写",
	"hash":      "不可变修订的内容哈希；省略时读取草稿",
	"section":   "schema 目录分区；省略时返回目录摘要",
	"type":      "schema 分区中的具体类型名",
	"from":      "比较的起始修订哈希",
	"to":        "比较的目标修订哈希",
}

// protocolCommands routes every authoring command to the shared versioned
// service. Imported configurations must be migrated before they can execute.
func protocolCommands() []*cliCommand {
	commands := []*cliCommand{}
	for _, spec := range []struct {
		action, summary, usage string
		flags                  []cliFlagSpec
	}{
		{"draft", "写入协议草稿并校验定义", "elysia protocol draft '<schemaVersion=2 JSON>'", nil},
		{"preview", "预览请求、响应、事件或工作流", "elysia protocol preview --sample '<JSON>' [--direction <direction>] [--sequence] [--mode mapping|session|task|models|agent]", []cliFlagSpec{{name: "sample"}, {name: "direction"}, {name: "sequence", boolean: true}, {name: "mode"}, {name: "sample-id"}, {name: "operation"}, {name: "kind"}, {name: "purpose"}}},
		{"test", "验证真实上游契约（受权限策略控制）", "elysia protocol test --operation <id> --sample '<semantic request JSON>' [--base-url <URL>] [--api-key <key>]", []cliFlagSpec{{name: "operation"}, {name: "sample"}, {name: "base-url"}, {name: "api-key"}}},
		{"models", "按声明操作验证真实上游模型目录（受权限策略控制）", "elysia protocol models [--base-url <URL>] [--api-key <key>]", []cliFlagSpec{{name: "base-url"}, {name: "api-key"}}},
		{"save", "保存草稿与离线证据，不启用", "elysia protocol save [--expected <draft-hash>]", []cliFlagSpec{{name: "expected"}}},
		{"read", "读取协议草稿或不可变修订", "elysia protocol read --id <id> [--hash <revision-hash>]", []cliFlagSpec{{name: "id"}, {name: "hash"}}},
	} {
		action := spec.action
		for index := range spec.flags {
			spec.flags[index].usage = protocolFlagUsage[spec.flags[index].name]
		}
		command := &cliCommand{group: "protocol", name: action, summary: spec.summary, usage: spec.usage, flags: spec.flags, detail: protocolV2DraftDetail,
			handler: func(s *Server) CLIHandler { return &protocolV2Tool{server: s, action: action} },
		}
		if action == "draft" {
			command.positionals = []cliPositionalSpec{{"config", "完整 v2 定义 JSON"}}
		}
		flags := append([]cliFlagSpec(nil), spec.flags...)
		command.mapper = func(inv *cliInvocation) (map[string]any, error) {
			params := map[string]any{}
			if action == "draft" {
				if len(inv.args) != 1 {
					return nil, fmt.Errorf("protocol draft requires one complete v2 JSON definition")
				}
				if err := inv.setJSONText(params, "config", inv.args[0]); err != nil {
					return nil, err
				}
			}
			for _, flag := range flags {
				key := flag.name
				switch key {
				case "base-url":
					key = "baseUrl"
				case "api-key":
					key = "apiKey"
				case "sample-id":
					key = "sample"
				case "sample":
					if inv.Has("sample") {
						if err := inv.setJSONText(params, "sampleRequest", inv.Str("sample")); err != nil {
							return nil, err
						}
					}
					continue
				}
				if flag.boolean {
					inv.setBool(params, flag.name, key)
				} else {
					inv.setStr(params, flag.name, key)
				}
			}
			return params, nil
		}
		commands = append(commands, command)
	}
	return append(commands, protocolV2Commands()...)
}
