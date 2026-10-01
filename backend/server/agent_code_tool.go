package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// 源码快照:构建脚本(scripts/build-standalone.mjs)在编译前把仓库源码
// (backend 的 Go 与预置 JSON、webui 的 src)同步到本目录(下划线前缀让 go 工具链忽略)并随 go:embed 打进
// 二进制——内置助手与 MCP 调用方经 `elysia code` 命令按仓库相对路径查看
// 引擎实现与当前版本预置协议原文,设计/修改协议时有据可依。仓库只跟踪
// README.md 占位(go:embed 要求目录非空);纯 `go build` 的开发构建里快照
// 为空,命令返回友好提示。
//
//go:embed all:_snapshot
var sourceSnapshotFS embed.FS

const sourceSnapshotRoot = "_snapshot"

// snapshotPlaceholderNotice 是快照未随构建打包时的提示(开发构建常态)。
const snapshotPlaceholderNotice = "本二进制未打包源码快照——快照由 scripts/build-standalone.mjs 在构建时同步;用该脚本重新构建后可查看"

// snapshotAvailable 判断快照是否包含实际内容(占位 README 不算)。
func snapshotAvailable() bool {
	entries, err := fs.ReadDir(sourceSnapshotFS, sourceSnapshotRoot)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return true
		}
		if entry.Name() != "README.md" {
			return true
		}
	}
	return false
}

// listSnapshotFiles 返回快照内的全部仓库相对路径,按字典序。
func listSnapshotFiles() ([]string, error) {
	var files []string
	err := fs.WalkDir(sourceSnapshotFS, sourceSnapshotRoot, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		files = append(files, strings.TrimPrefix(p, sourceSnapshotRoot+"/"))
		return nil
	})
	sort.Strings(files)
	return files, err
}

// ---- code ls ----

type codeListTool struct{ server *Server }

func (t *codeListTool) CLIEffect() string { return "" }

func (t *codeListTool) Description() string {
	return "列出随二进制打包的源码快照文件(仓库相对路径,可加路径前缀过滤;配合 grep/head 管道使用)。"
}

func (t *codeListTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	if !snapshotAvailable() {
		return CLIResult{OK: true, Summary: snapshotPlaceholderNotice, Data: map[string]any{"files": []string{}}}
	}
	var params struct {
		Prefix string `json:"prefix"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}
	files, err := listSnapshotFiles()
	if err != nil {
		return CLIError("读取快照失败: "+err.Error(), "snapshot_failed")
	}
	prefix := strings.TrimSpace(params.Prefix)
	matched := make([]string, 0, len(files))
	for _, file := range files {
		if prefix == "" || strings.HasPrefix(file, prefix) {
			matched = append(matched, file)
		}
	}
	return CLIResult{OK: true,
		Summary: fmt.Sprintf("源码快照共 %d 个文件,匹配 %d 个(用 code read <路径> 查看)", len(files), len(matched)),
		Data:    map[string]any{"files": matched}}
}

// ---- code read ----

type codeReadTool struct{ server *Server }

func (t *codeReadTool) CLIEffect() string { return "" }

func (t *codeReadTool) Description() string {
	return "读取源码快照中的一个文件(仓库相对路径,如 backend/server/custom_protocol.go 或 packages/webui/src/lib/types.ts);" +
		"输出较长时配合 grep/head 管道截取。预置协议原文在 backend/server/presets/ 下。"
}

func (t *codeReadTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	var params struct {
		Path string `json:"path"`
	}
	if badRequest, ok := decodeCLIArgs(args, &params); !ok {
		return badRequest
	}
	requested := strings.TrimSpace(params.Path)
	if requested == "" {
		return CLIError("path 必填(仓库相对路径,先用 code ls 浏览)", "missing_path")
	}
	cleaned := path.Clean(strings.TrimPrefix(strings.ReplaceAll(requested, "\\", "/"), "/"))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return CLIError(fmt.Sprintf("非法路径 %q(须为仓库内相对路径)", requested), "invalid_path")
	}
	// 输入校验先于快照可用性:路径拒绝与环境状态无关。
	if !snapshotAvailable() {
		return CLIResult{OK: false, Summary: snapshotPlaceholderNotice, Data: map[string]any{"error": "snapshot_unavailable"}}
	}
	content, err := sourceSnapshotFS.ReadFile(path.Join(sourceSnapshotRoot, cleaned))
	if err != nil {
		// 未命中时给最接近的提示,减少模型来回试错。
		files, listErr := listSnapshotFiles()
		hint := ""
		if listErr == nil {
			base := path.Base(cleaned)
			for _, file := range files {
				if path.Base(file) == base || strings.Contains(file, cleaned) {
					if hint == "" {
						hint = file
					} else {
						hint = hint + ", " + file
					}
					if len(hint) > 200 {
						break
					}
				}
			}
		}
		message := fmt.Sprintf("快照中不存在 %q", requested)
		if hint != "" {
			message += ";相近路径: " + hint
		}
		return CLIError(message, "not_found")
	}
	lines := strings.Count(string(content), "\n") + 1
	return CLIResult{OK: true,
		Summary: fmt.Sprintf("%s(%d 行,%d 字节)", cleaned, lines, len(content)),
		Data:    map[string]any{"path": cleaned, "content": string(content)}}
}
