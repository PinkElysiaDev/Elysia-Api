package server

import (
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2acompat/a2av0"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleAgentCard(c *gin.Context) {
	version, ok := a2aVersion(c)
	if !ok {
		return
	}
	base := strings.TrimRight(s.publicBaseURL(c), "/")
	card := &a2a.AgentCard{
		Name: "Elysia API Gateway Agent", Version: "1.0.0",
		Description:       "Elysia API 网关内置智能体的远程运维面：通过任务驱动网关配置与诊断。",
		Capabilities:      a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes: []string{"text/plain"}, DefaultOutputModes: []string{"text/plain", "application/json"},
		Provider: &a2a.AgentProvider{Org: "Elysia API", URL: base},
		SupportedInterfaces: []*a2a.AgentInterface{
			{URL: base + "/a2a", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version},
			{URL: base + "/a2a", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2av0.Version},
		},
		SecuritySchemes:      a2a.NamedSecuritySchemes{"bearerAuth": a2a.HTTPAuthSecurityScheme{Scheme: "bearer", Description: "网关签发的 API Key（需 agent 作用域）"}},
		SecurityRequirements: a2a.SecurityRequirementsOptions{{"bearerAuth": {}}},
		Skills: []a2a.AgentSkill{{ID: "elysia-gateway-ops", Name: "网关运维与远程配置",
			Description: "驱动 Elysia API 网关内置 AI 助手：配置模型源/模型组/API Key/自定义协议、拉取模型列表、查询用量与排障。等待审批时任务进入 input-required，用带 data part（approved/answer/note）的消息续发即可恢复。",
			Tags:        []string{"gateway", "configuration", "ops", "ai-agent"}, InputModes: []string{"text/plain"}, OutputModes: []string{"text/plain", "application/json"},
		}},
	}
	if version == a2av0.Version {
		a2asrv.NewAgentCardHandler(a2av0.NewStaticAgentCardProducer(card)).ServeHTTP(c.Writer, c.Request)
	} else {
		a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(c.Writer, c.Request)
	}
}
