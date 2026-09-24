package server

import (
	"fmt"
	"sort"
	"strings"
)

// elysia help 体系：与命令表同源生成，三级渐进发现——
//
//	elysia help               → 组级一览 + 常用组合示例
//	elysia help <group>       → 组内命令 + flag 全表
//	elysia help <group> <cmd> → 用法行、flag/位置参数全说明、示例与完整语义
//	  （完整语义直接复用目标工具的 Definition().Description——比旧工具描述
//	   更详尽的细节放在这一层，而不是常驻提示词。）

// renderCLIHelp 按 help 参数层级渲染（args 为 help 之后的词）。
func renderCLIHelp(args []string) string {
	table := cliCommandTable()
	if len(args) == 0 {
		return helpOverview(table)
	}
	// help <group>：组级。
	if len(args) == 1 {
		group := args[0]
		if group == "help" {
			return "elysia help [group] [command]：分级查看命令参考。"
		}
		if !hasGroup(table, group) {
			return unknownGroupMessage(table, group)
		}
		return helpGroup(table, group)
	}
	// help <group> <command...>：命令级（二级命令拼空格）。
	path := strings.Join(args, " ")
	for _, command := range table {
		if command.Path() == path {
			return helpCommand(command)
		}
	}
	group := args[0]
	if hasGroup(table, group) {
		return fmt.Sprintf("组 %s 下没有命令 %q（运行 elysia help %s 查看组内命令）", group, strings.Join(args[1:], " "), group)
	}
	return unknownGroupMessage(table, group)
}

func hasGroup(table []*cliCommand, group string) bool {
	for _, command := range table {
		if command.group == group {
			return true
		}
	}
	return false
}

func unknownGroupMessage(table []*cliCommand, group string) string {
	groups := groupNames(table)
	return fmt.Sprintf("没有命令组 %q。可用组：%s", group, strings.Join(groups, "、"))
}

func groupNames(table []*cliCommand) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, command := range table {
		if !seen[command.group] {
			seen[command.group] = true
			names = append(names, command.group)
		}
	}
	return names
}

// helpOverview 组级一览。
func helpOverview(table []*cliCommand) string {
	var b strings.Builder
	b.WriteString("elysia —— 网关运维 CLI（全部操作经 bash 工具执行）\n\n")
	b.WriteString("命令组：\n")
	for _, name := range groupNames(table) {
		commands := commandsOfGroup(table, name)
		summaries := make([]string, 0, len(commands))
		for _, command := range commands {
			summaries = append(summaries, command.name)
		}
		b.WriteString(fmt.Sprintf("  %-10s %s（%s）\n", name, groupSummary(name), strings.Join(summaries, ", ")))
	}
	b.WriteString("\n分级帮助：elysia help <组>（flag 全表）/ elysia help <组> <命令>（完整语义与示例）。\n")
	b.WriteString("\n语法：支持 '引号'、--flag value 或 --flag=value、批处理（&& 失败即停；; 或换行继续）、\n")
	b.WriteString("尾管道（| grep <子串> 过滤、| head <n> 截前 n 行）。\n")
	b.WriteString("\n常用组合示例：\n")
	b.WriteString("  elysia source ls && elysia model ls --source 主源 --limit 20\n")
	b.WriteString("  elysia usage logs --days 1 --status failed | head 10\n")
	b.WriteString("  elysia group create --name 主力 --models s1:gpt-4o\n")
	return b.String()
}

// groupSummary 各命令组的一句话定位（help 总览用）。
func groupSummary(group string) string {
	switch group {
	case "source":
		return "模型源管理"
	case "model":
		return "单模型管理"
	case "group":
		return "模型组管理与成员维护"
	case "key":
		return "API Key（推理访问令牌）管理"
	case "protocol":
		return "自定义协议设计（草稿/离线预览/真实测试/保存）"
	case "usage":
		return "用量统计与调用日志"
	case "syslog":
		return "系统日志"
	case "outbound":
		return "出站禁止 IP 段（SSRF 防护）"
	case "session":
		return "会话操作"
	default:
		return group
	}
}

func commandsOfGroup(table []*cliCommand, group string) []*cliCommand {
	commands := []*cliCommand{}
	for _, command := range table {
		if command.group == group {
			commands = append(commands, command)
		}
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].name < commands[j].name })
	return commands
}

// helpGroup 组级帮助：命令清单 + flag 全表。
func helpGroup(table []*cliCommand, group string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("elysia %s — %s\n\n", group, groupSummary(group)))
	for _, command := range commandsOfGroup(table, group) {
		b.WriteString(fmt.Sprintf("  %s\n    %s\n", command.usage, command.summary))
		if len(command.flags) > 0 {
			for _, flag := range command.flags {
				b.WriteString(fmt.Sprintf("      --%-22s %s\n", flag.name, flag.usage))
			}
		}
		for _, positional := range command.positionals {
			b.WriteString(fmt.Sprintf("      %-24s %s\n", "<"+positional.name+">", positional.usage))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("完整语义与示例：elysia help %s <命令>。\n", group))
	return b.String()
}

// helpCommand 命令级帮助：用法 + flag + 位置参数 + 示例 + 目标工具完整描述。
func helpCommand(command *cliCommand) string {
	var b strings.Builder
	b.WriteString(command.usage + "\n" + command.summary + "\n\n")
	if len(command.flags) > 0 {
		b.WriteString("参数：\n")
		for _, flag := range command.flags {
			b.WriteString(fmt.Sprintf("  --%-22s %s\n", flag.name, flag.usage))
		}
	}
	for _, positional := range command.positionals {
		b.WriteString(fmt.Sprintf("  %-24s %s\n", "<"+positional.name+">", positional.usage))
	}
	if command.example != "" {
		b.WriteString("\n示例：" + command.example + "\n")
	}
	if detail := command.tool(nil).Definition().Description; detail != "" {
		b.WriteString("\n详细说明：" + detail + "\n")
	}
	// 门控提示与目标工具同源。
	if tool := command.tool(nil); tool.Gated() {
		b.WriteString(fmt.Sprintf("\n此命令受审批门控（权限键 %s），执行前会暂停等待用户确认。\n", tool.PermissionKey()))
	}
	return b.String()
}
