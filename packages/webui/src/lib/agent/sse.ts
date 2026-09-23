import { getToken } from "../auth";
import type { AgentStreamEvent } from "./types";

/**
 * 通用 SSE 客户端：fetch POST + ReadableStream 解析 `event:`/`data:` 行。
 * EventSource 不支持 POST 与 Authorization 头，故手写流解析。
 * 服务端事件 data 为单行 JSON（后端保证），此处按多行 data 拼接兜底。
 */
export async function streamAgentEvents(
  url: string,
  body: unknown,
  onEvent: (event: AgentStreamEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (token) headers.Authorization = `Bearer ${token}`;

  const response = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
    signal,
  });
  if (response.status === 409) {
    const payload = await response.json().catch(() => null);
    const code = payload?.error?.code ?? "session_running";
    const message = payload?.error?.message ?? "会话已有轮次进行中";
    throw new AgentStreamError(code, message, 409);
  }
  if (!response.ok || !response.body) {
    const text = await response.text().catch(() => "");
    let message = `请求失败（${response.status}）`;
    try {
      const payload = JSON.parse(text);
      if (payload?.error?.message) message = payload.error.message;
    } catch {
      /* keep default */
    }
    throw new AgentStreamError("stream_failed", message, response.status);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  const dispatch = (block: string) => {
    let eventType = "";
    const dataLines: string[] = [];
    for (const line of block.split("\n")) {
      if (line.startsWith("event: ")) eventType = line.slice(7).trim();
      else if (line.startsWith("data: ")) dataLines.push(line.slice(6));
    }
    if (!eventType || dataLines.length === 0) return;
    try {
      const parsed = JSON.parse(dataLines.join("\n")) as AgentStreamEvent;
      parsed.type = eventType as AgentStreamEvent["type"];
      onEvent(parsed);
    } catch {
      /* 单事件解析失败容忍 */
    }
  };

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let separator = buffer.indexOf("\n\n");
    while (separator >= 0) {
      dispatch(buffer.slice(0, separator));
      buffer = buffer.slice(separator + 2);
      separator = buffer.indexOf("\n\n");
    }
  }
  if (buffer.trim()) dispatch(buffer);
}

class AgentStreamError extends Error {
  code: string;
  status: number;
  constructor(code: string, message: string, status: number) {
    super(message);
    this.code = code;
    this.status = status;
  }
}
