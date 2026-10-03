package server

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/protocol"
)

const (
	agentDocMax      = 20
	agentDocMaxText  = 512 << 10
	agentDocMaxFile  = 8 << 20
	agentDocMaxTotal = 32 << 20
)

type agentUserContentRenderer struct{}

func newAgentUserContentRenderer(_ *Server) *agentUserContentRenderer {
	return &agentUserContentRenderer{}
}

func (*agentUserContentRenderer) RenderUserContent(_ agent.SessionMeta, content *agent.UserContent) ([]protocol.Node, error) {
	if len(content.Documents) > agentDocMax {
		return nil, fmt.Errorf("附件最多 %d 个", agentDocMax)
	}
	if len(content.Text) > agentDocMaxText {
		return nil, fmt.Errorf("用户文本超过 %d 字节上限", agentDocMaxText)
	}
	var nodes []protocol.Node
	if content.Text != "" {
		nodes = append(nodes, protocol.Node{Kind: protocol.TextNode, Payload: protocol.StringValue(content.Text)})
	}
	total := len(content.Text)
	for index, document := range content.Documents {
		parts, size, err := renderAgentDocument(index, document)
		if err != nil {
			return nil, err
		}
		total += size
		if total > agentDocMaxTotal {
			return nil, fmt.Errorf("输入材料总量超过 %d MiB 上限", agentDocMaxTotal>>20)
		}
		nodes = append(nodes, parts...)
	}
	return nodes, nil
}

func renderAgentDocument(index int, document agent.Document) ([]protocol.Node, int, error) {
	label := document.Name
	if label == "" {
		label = fmt.Sprintf("材料 %d", index+1)
	}
	if document.Text != "" {
		if len(document.Text) > agentDocMaxText {
			return nil, 0, fmt.Errorf("材料 %s 文本超过 %d 字节上限", label, agentDocMaxText)
		}
		text := fmt.Sprintf("\n===== 材料 %d：%s =====\n%s\n===== 材料 %d 结束 =====", index+1, label, document.Text, index+1)
		return []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(text)}}, len(document.Text), nil
	}
	if document.DataURL == "" {
		return nil, 0, nil
	}
	mime, payload, decoded, err := parseAgentDataURL(document.DataURL)
	if err != nil {
		return nil, 0, fmt.Errorf("材料 %s: %w", label, err)
	}
	if len(decoded) > agentDocMaxFile {
		return nil, 0, fmt.Errorf("材料 %s 超过 %d MiB 上限", label, agentDocMaxFile>>20)
	}
	if mime == "" {
		mime = document.Mime
	}
	if mime == "" || (document.Mime != "" && document.Mime != mime) {
		return nil, 0, fmt.Errorf("材料 %s MIME 类型缺失或不一致", label)
	}
	kind := protocol.DocumentNode
	if strings.HasPrefix(mime, "image/") {
		kind = protocol.ImageNode
	}
	media, _ := protocol.EncodeValue(protocol.Object{"mime": protocol.StringValue(mime), "data": protocol.StringValue(payload)})
	// The attachment label is part of the Agent prompt, avoiding substitution of
	// one provider's filename field for another provider's document title.
	return []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(label)}, {Kind: kind, Payload: media}}, len(decoded), nil
}

func parseAgentDataURL(dataURL string) (mime string, payload string, decoded []byte, err error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", "", nil, fmt.Errorf("dataUrl 必须以 data: 开头")
	}
	comma := strings.Index(dataURL, ",")
	if comma < 0 {
		return "", "", nil, fmt.Errorf("dataUrl 缺少逗号分隔符")
	}
	header := dataURL[5:comma]
	payload = dataURL[comma+1:]
	if !strings.HasSuffix(header, ";base64") {
		return "", "", nil, fmt.Errorf("dataUrl 仅支持 base64 编码")
	}
	mime = strings.TrimSuffix(header, ";base64")
	decoded, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("base64 解码失败: %w", err)
	}
	return mime, payload, decoded, nil
}
