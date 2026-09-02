package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

// relayFailer 把统一的错误语义写入该入口的响应格式并落库：
// typed=false 写扁平 {"error": msg}（chat/completions 线制），
// typed=true 写 OpenAI 错误对象 {"error":{message,type}}（responses 线制）。
type relayFailer struct {
	s         *Server
	c         *gin.Context
	record    *usageRecord
	startTime time.Time
	typed     bool
}

func (f relayFailer) fail(statusCode int, errType, errKind, errMsg string) {
	if f.typed {
		f.s.failRequestTypedKind(f.c, f.record, f.startTime, statusCode, errType, errKind, errMsg)
		return
	}
	f.s.failRequestKind(f.c, f.record, f.startTime, statusCode, errKind, errMsg)
}

// relayPlan 是前置阶段（鉴权→组校验→候选→能力约束→预估→限流）全部
// 就绪后的转发计划。releaseLimiter 由调用方 defer 释放。
type relayPlan struct {
	group              *config.ModelGroupConfig
	candidates         []config.ModelRef
	filtered           bool
	filteredModalities []string
	estimatedTokens    int
	releaseLimiter     func()
}

// prepareRelayPlan 完成 chatCompletions 与 responses 共用的前置阶段：
//
//	组级鉴权 → 组校验 → 候选构建/亲和置顶/能力软过滤/多 key 展开 →
//	组级约束（MaxTokens 覆盖[chat 线制]、tools 拒绝、多模态过滤）→
//	用量预估 → 组级限流。
//
// 任一步失败经 failer 写响应并落库后返回 ok=false。两入口此前各持一份
// ~90 行的近克隆前置段，任何策略调整都要改两处且极易漂移——收敛于此。
func (s *Server) prepareRelayPlan(
	c *gin.Context,
	record *usageRecord,
	startTime time.Time,
	maheshvaraReq *relay.MaheshvaraRequest,
	failer relayFailer,
	applyGroupMaxTokens bool,
) (*relayPlan, bool) {
	// 模型组级访问权限：先于 validateModelGroup 校验请求的模型组名，
	// 这样即使目标组为空/未配置，越权访问也返回 403（而非泄露组的存在性/状态）。
	if !s.tokenAllowsGroup(c, maheshvaraReq.Model) {
		failer.fail(http.StatusForbidden, "permission_error", "",
			fmt.Sprintf("api key is not allowed to access model group '%s'", maheshvaraReq.Model))
		return nil, false
	}

	group, err := s.validateModelGroup(maheshvaraReq.Model)
	if err != nil {
		failer.fail(statusForGroupError(err), "invalid_request_error", "", err.Error())
		return nil, false
	}
	setRecordGroup(record, group)

	// 构建有序候选模型列表，按模型组策略排列。失败时逐个故障转移。
	candidates := s.buildCandidates(group)
	if len(candidates) == 0 {
		failer.fail(http.StatusInternalServerError, "api_error", "",
			fmt.Sprintf("no available models in group '%s'", group.Name))
		return nil, false
	}
	// 渠道亲和性：把该 key+group 上次成功的模型提到候选最前（短 TTL 粘连），
	// 提升上游 prompt 缓存命中率。不改变候选集合，故障转移逻辑不受影响。
	if sticky := s.affinity.get(record.KeyHash, group.ID, startTime); sticky != "" {
		candidates = applyAffinity(candidates, sticky)
	}
	// 组内候选软过滤（方向2）：按请求内容把不支持所需能力的候选移到末尾。
	candidates = reorderCandidatesByRequestNeeds(candidates,
		maheshvaraRequestHasMultimodalInput(maheshvaraReq), maheshvaraRequestUsesTools(maheshvaraReq))
	// 多 key 展开（方向6）：按源策略把候选解析为逐次尝试序列（single 时原样）。
	candidates = s.expandCandidatesByKeyStrategy(candidates)

	// 组级 MaxTokens 覆盖客户端值（chat 线制启用；responses 线制维持原值）。
	if applyGroupMaxTokens && group.MaxTokens > 0 {
		maheshvaraReq.MaxOutputTokens = group.MaxTokens
	}
	// 组级 tools 能力落地（方向2）：组声明不支持工具而请求携带工具定义/工具消息时，
	// 400 拒绝并明确报错——静默剥离会破坏 agent 循环语义（已确认的产品决策）。
	if rejectToolRequestsIfNeeded(group, maheshvaraReq) {
		failer.fail(http.StatusBadRequest, "invalid_request_error", "",
			fmt.Sprintf("model group '%s' does not support tool calling, but the request contains tools or tool messages", group.Name))
		return nil, false
	}
	filtered, filteredParts, filteredModalities := filterMaheshvaraMultimodalInputsIfNeeded(group, maheshvaraReq)
	if filtered {
		s.logVerbose("[Maheshvara Multimodal Filter] group=%s filteredParts=%d modalities=%v", group.Name, filteredParts, filteredModalities)
		// 过滤是原地变更：dump 变更后的请求，否则排查时只能看到未过滤版
		//（入口处的 [Maheshvara Request] dump 于此无效）。
		if maheshvaraJSON, err := json.Marshal(maheshvaraReq); err == nil {
			s.logVerbose("[Maheshvara Request After Multimodal Filter] %s", compactLogJSON(maheshvaraJSON))
		}
		// 让客户端可感知剥离行为（非纯静默）：形如 "image,audio"。
		c.Writer.Header().Set("X-Elysia-Filtered-Modalities", strings.Join(filteredModalities, ","))
	}

	estimatedUsage := estimateMaheshvaraRequestUsage(maheshvaraReq, s.config.GetUsageConfig())
	record.Usage = usageTokenUsageFromMaheshvara(estimatedUsage)
	record.UsageDetail = usageDetailFromMaheshvara(estimatedUsage)
	record.UsageSource = estimatedUsage.Source

	releaseLimiter, err := s.acquireRateLimit(group, estimatedUsage.EstimatedTotalTokens)
	if err != nil {
		failer.fail(http.StatusTooManyRequests, "rate_limit_error", "", err.Error())
		return nil, false
	}
	return &relayPlan{
		group:              group,
		candidates:         candidates,
		filtered:           filtered,
		filteredModalities: filteredModalities,
		estimatedTokens:    estimatedUsage.EstimatedTotalTokens,
		releaseLimiter:     releaseLimiter,
	}, true
}
