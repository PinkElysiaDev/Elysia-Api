package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/storage"
)

const (
	agentStreamMaxOutputTokens = 8192
	agentCallTimeoutSec        = 300
)

type agentStreamCaller struct{ server *Server }

func newAgentStreamCaller(s *Server) *agentStreamCaller { return &agentStreamCaller{server: s} }
func applyAgentPermittedKey(ctx context.Context, store *storage.Store, model *storage.Model) error {
	sources, err := store.ListSources(ctx)
	if err != nil {
		return fmt.Errorf("read model source permissions: %w", err)
	}
	for _, source := range sources {
		if source.ID != model.SourceID {
			continue
		}
		effective := source.EffectiveKeys()
		if len(effective) == 0 {
			return nil
		}
		for _, key := range effective {
			// 权限集存的是拉取到的模型 ID；助手按 Name/ID 双匹配解析模型，
			// 两个标识任一命中即可。
			if key.KeyAllowsModel(model.ID) || key.KeyAllowsModel(model.Name) {
				model.APIKey = key.Value
				return nil
			}
		}
		return fmt.Errorf("模型源 %q 的所有 key 都无权服务模型 %q（按 key 拉取分组）", source.Name, model.Name)
	}
	return nil
}

func (c *agentStreamCaller) Call(ctx context.Context, req agent.CallRequest, cb agent.StreamCallbacks) (*agent.CallResult, error) {
	if err := c.server.protocolRuntimeError(); err != nil {
		return nil, err
	}
	store := c.server.store
	if store == nil {
		return nil, fmt.Errorf("sqlite store is unavailable")
	}
	model, found := findCustomProtocolTestModel(ctx, store, req.ModelSourceID, req.Model)
	if !found {
		if strings.TrimSpace(req.ModelSourceID) == "" || strings.TrimSpace(req.Model) == "" {
			// 会话未配置模型：给模型可行动的指引，而不是渲染成一串空引号。
			return nil, fmt.Errorf("会话未配置模型（模型源/模型名为空）——请提醒用户在会话设置中选择模型源与模型后重试")
		}
		return nil, fmt.Errorf("模型源 %q 下没有找到模型 %q（源不存在或模型清单未刷新；可用 elysia source ls / elysia model ls 核对，必要时 elysia source refresh 拉取）", req.ModelSourceID, req.Model)
	}
	if err := applyAgentPermittedKey(ctx, store, &model); err != nil {
		return nil, err
	}

	return c.callBoundProtocol(ctx, req, model, cb)
}
