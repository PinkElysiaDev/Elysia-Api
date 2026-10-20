package server

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// cliFlagValue is a pflag value preserving gateway semantics: explicit empty
// values, yes/no booleans, and repeated comma-delimited lists (not CSV).
type cliFlagValue struct {
	spec   cliFlagSpec
	values map[string]string
}

func (v *cliFlagValue) String() string { return v.values[v.spec.name] }
func (v *cliFlagValue) Type() string {
	if v.spec.boolean {
		return "bool"
	}
	if v.spec.list {
		return "list"
	}
	return "string"
}
func (v *cliFlagValue) Set(value string) error {
	if v.spec.boolean {
		if _, ok := cliBoolValue(value); !ok {
			return fmt.Errorf("需要布尔值")
		}
	}
	if old, ok := v.values[v.spec.name]; ok && v.spec.list {
		value = old + "," + value
	}
	v.values[v.spec.name] = value
	return nil
}

// Every request gets its own Cobra tree and flag values. Execution and approval
// probing use this same resolver; Cobra never invokes a business handler.
func cliResolveRequest(args []string) (*cliInvocation, string, error) {
	var output bytes.Buffer
	var invocation *cliInvocation
	root := &cobra.Command{
		Use:           cliName,
		Short:         "网关运维 CLI（全部操作经 elysia_cli 工具执行）",
		Long:          cliSyntaxHelp,
		SilenceErrors: true, SilenceUsage: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(append([]string{}, args...))
	nodes := map[string]*cobra.Command{"": root}
	specs := map[*cobra.Command]*cliCommand{}
	for _, spec := range cliCommandTable() {
		parent, path := root, ""
		for _, name := range strings.Fields(spec.Path()) {
			path = strings.TrimSpace(path + " " + name)
			node := nodes[path]
			if node == nil {
				node = &cobra.Command{Use: name, Short: groupSummary(name)}
				parent.AddCommand(node)
				nodes[path] = node
			}
			parent = node
		}
		cmd := parent
		specs[cmd] = spec
		cmd.Short = spec.summary
		cmd.Example = spec.example
		if cmd.Example != "" {
			cmd.Example = "  示例：" + cmd.Example
		}
		for _, positional := range spec.positionals {
			cmd.Use += " [" + positional.name + "]"
		}
		values := map[string]string{}
		for _, flag := range spec.flags {
			cmd.Flags().Var(&cliFlagValue{spec: flag, values: values}, flag.name, flag.usage)
			if flag.boolean {
				cmd.Flags().Lookup(flag.name).NoOptDefVal = "true"
			}
		}
		cmd.Args = func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(len(spec.positionals))(cmd, args); err != nil {
				return err
			}
			for _, arg := range args {
				if arg == "" {
					return fmt.Errorf("位置参数不能为空（检查引号内容）")
				}
			}
			return nil
		}
		cmd.RunE = func(_ *cobra.Command, args []string) error {
			invocation = &cliInvocation{command: spec, args: args, flags: values}
			return nil
		}
	}
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if spec := specs[cmd]; spec != nil {
			detail := ""
			if spec.detail != nil {
				detail = spec.detail()
			} else {
				detail = cliDescriptionOf(spec.handler(nil))
			}
			cmd.Long = spec.summary + "\n\n" + spec.usage
			for _, positional := range spec.positionals {
				cmd.Long += "\n<" + positional.name + ">: " + positional.usage
			}
			if detail != "" {
				cmd.Long += "\n\n详细说明：" + detail
			}
		}
		defaultHelp(cmd, args)
	})
	_, err := root.ExecuteC()
	return invocation, output.String(), err
}

func cliResolve(args []string) (*cliInvocation, error) {
	inv, _, err := cliResolveRequest(args)
	if err == nil && inv == nil {
		err = fmt.Errorf("需要可执行的 CLI 命令")
	}
	return inv, err
}
