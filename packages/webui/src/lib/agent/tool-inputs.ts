import type { AgentAssistantContent, AgentMessage, AgentToolResultContent } from "./types";

/** 兼容旧结果缺失 input：按同轮 callId + 工具名匹配助手的原始调用。 */
export function withToolInputs(messages: AgentMessage[]): AgentMessage[] {
  const inputs = new Map<string, { name?: string; input: unknown }>();
  return messages.map((message) => {
    if (message.role === "user") inputs.clear();
    if (message.role === "assistant") {
      for (const call of (message.content as AgentAssistantContent).toolCalls ?? []) {
        if (!call.id) continue;
        let input = call.arguments ?? call.arguments_text;
        if (typeof input === "string") {
          try { input = JSON.parse(input); } catch { /* 非 JSON 参数按原始文本展示。 */ }
        }
        inputs.set(call.id, { name: call.name, input });
      }
    }
    if (message.role !== "tool_result") return message;
    const content = message.content as AgentToolResultContent;
    const original = inputs.get(content.callId);
    if (content.input != null || original?.name !== content.name || original.input == null) return message;
    return { ...message, content: { ...content, input: original.input } };
  });
}
