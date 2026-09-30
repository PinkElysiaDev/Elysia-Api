# 文档

[项目首页](../README.md) · **简体中文** · [English](README.en.md)

Elysia API 的部署、协议、接口和开发参考。首次使用从[快速开始](../README.md#快速开始)进入。

## 部署与运维

| 文档 | 内容 |
| --- | --- |
| [部署指南](deployment.md) | 二进制、macOS App、Docker、配置、日志、升级与恢复 |

## 协议与接入

| 文档 | 内容 |
| --- | --- |
| [Maheshvara](maheshvara-protocol.md) | 内部模型、协议映射、流式和能力边界 |
| [自定义协议](protocol-definition-reference.md) | JSON 定义、字段映射、条件、流式帧和模型发现 |

## 管理接口

| 文档 | 内容 |
| --- | --- |
| [管理 API](webui-api.md) | 配置、模型源、模型组、令牌、用量与日志 |
| [数据模型](webui-data-model.md) | 字段语义、标识、秘密字段、分页和类型来源 |
| [远程 Agent](remote-agent-api.md) | REST、MCP、A2A、鉴权、会话与审批 |
| [CLI 参考](agent-cli.md) | `elysia_cli` 命令、参数、批处理与限制 |
| [Agent 工具](agent-tools-catalog.md) | 工具职责、帮助来源和权限边界 |

## 开发与验证

| 文档 | 内容 |
| --- | --- |
| [开发指南](development.md) | 环境、本地开发、测试、交叉编译与发布产物 |
| [WebUI 开发](../packages/webui/README.md) | 前端命令、代理、构建与交互约定 |
| [macOS 验证](macos-testing.md) | 原生工具链、自动检查与真机验收 |
| [登录素材制作](../scripts/login-trace/README.md) | 提取、审图、资产校验与性能复现 |

各发布版本的变更见[更新日志](../CHANGELOG.md)。

## 阅读约定

- 文档描述当前检出版本；旧版本请切换到对应 Git tag。
- 原路径为中文，`.en.md` 为英文；字段名、命令、端点和协议标识保持原样。
- `<RELAY_TOKEN>` 等尖括号内容是需要替换的占位符；带省略号的结构标为说明性片段。
- 面板令牌、推理令牌和 Agent Key 各有用途；不要混用。详见[鉴权](remote-agent-api.md#鉴权)。
- 发现差异时以路由、类型、配置加载器和测试核对；文档维护方式见[开发指南](development.md#文档维护)。
