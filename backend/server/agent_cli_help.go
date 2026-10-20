package server

import (
	"sort"
	"strings"
)

const cliSyntaxHelp = `elysia —— 网关运维 CLI（全部操作经 elysia_cli 工具执行）

分级帮助：elysia help <组> / elysia help <组> <命令>；命令后也可使用 --help 或 -h。
语法由 mvdan/sh 解析：单引号、双引号、反斜杠转义、注释；空引号表示空值。
参数由 Cobra/pflag 解析：--flag value / --flag=value；布尔值可省略；列表支持重复与逗号分隔。
批处理：&& 失败跳过所在链；分号或换行继续。尾管道：| grep <子串>（大小写不敏感），
| head <n> / -n N / -N（文本行数，不是记录数）。不执行系统 shell、变量展开、命令替换、重定向或控制结构。
含 $、反引号或 shell 运算符的字面值请用单引号；通配符和 ~ 保持字面值。
批处理无整体事务或自动回滚；需要根据中间结果决定后续参数时应分次调用。
查询优先使用命令过滤参数和 --limit；输出可能截断。修改、真实出站和删除受服务端权限与业务策略控制。

常用组合示例：
  elysia source ls && elysia model ls --source 主源 --limit 20
  elysia usage logs --days 1 --status failed --limit 10
  elysia group create --name 主力 --models s1:gpt-4o`

func renderCLIHelp(args []string) string {
	_, text, err := cliResolveRequest(append([]string{"help"}, args...))
	if err != nil {
		return err.Error() + "\n"
	}
	return text
}

// Documentation uses the same Cobra help path as live calls.
func helpCommand(command *cliCommand) string { return renderCLIHelp(strings.Fields(command.Path())) }

func groupNames(table []*cliCommand) []string {
	seen := map[string]bool{}
	var names []string
	for _, command := range table {
		if !seen[command.group] {
			seen[command.group] = true
			names = append(names, command.group)
		}
	}
	return names
}

func commandsOfGroup(table []*cliCommand, group string) []*cliCommand {
	var commands []*cliCommand
	for _, command := range table {
		if command.group == group {
			commands = append(commands, command)
		}
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].name < commands[j].name })
	return commands
}

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
		return "协议设计与运维（schema/草稿/验证/预览/测试/修订/启用/回滚）"
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
