import { ExternalLink, FileCode2, History } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { tryParseJSON } from "@/lib/utils";
import { Collapse, JsonBlock } from "./ui-blocks";
import { EmptyHint } from "./activity-view";
import type { AgentSession } from "@/lib/agent/types";

/** 配置页：协议草稿的结构化详情（请求/响应/流式映射 + 完整 JSON）+ 跳转设计器。 */
const DRAFT_SECTIONS: { key: string; label: string }[] = [
  { key: "request", label: "请求构造（网关 → 上游）" },
  { key: "response", label: "响应解析（上游 → 网关）" },
  { key: "stream", label: "流式解析（SSE 帧）" },
];

export function DraftView({
  session,
  busy,
  onRestore,
}: {
  session: AgentSession;
  busy: boolean;
  onRestore: () => void;
}) {
  const navigate = useNavigate();
  const draft = session.draftConfig;
  const draftText =
    draft == null
      ? ""
      : typeof draft === "string"
        ? draft
        : JSON.stringify(draft, null, 2);
  const draftObject =
    draft && typeof draft === "object" && !Array.isArray(draft)
      ? (draft as Record<string, unknown>)
      : null;
  const protocolId = draftObject ? String(draftObject.id ?? "") : "";
  const protocolName = draftObject ? String(draftObject.name ?? "") : "";
  const restoreText =
    session.draftRestore == null ? "" : JSON.stringify(session.draftRestore);
  const canRestore =
    restoreText !== "" &&
    draftText !== "" &&
    restoreText !== JSON.stringify(draft ?? null) &&
    !busy;

  if (!draftText) {
    return (
      <EmptyHint
        icon={<FileCode2 className="h-4 w-4" />}
        text="助手提交协议草稿后，可在这里查看配置详情与映射关系。"
      />
    );
  }

  const sections = draftObject
    ? DRAFT_SECTIONS.filter((section) => draftObject[section.key] != null)
    : [];
  const basicEntries = draftObject
    ? Object.entries(draftObject).filter(
        ([key]) => !DRAFT_SECTIONS.some((section) => section.key === key),
      )
    : [];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-w-0 items-center gap-2 px-1 py-1.5">
        {protocolId ? (
          <Badge variant="outline" className="shrink-0 font-mono text-2xs">
            {protocolId}
          </Badge>
        ) : null}
        {protocolName ? (
          <span className="min-w-0 truncate text-xs text-muted-foreground">
            {protocolName}
          </span>
        ) : null}
      </div>
      <div className="no-scrollbar min-h-0 flex-1 space-y-2 overflow-y-auto px-1 pb-2">
        {basicEntries.length > 0 ? (
          <Collapse title="基本信息" defaultOpen>
            <JsonBlock
              value={Object.fromEntries(basicEntries)}
              maxHeight="max-h-44"
            />
          </Collapse>
        ) : null}
        {sections.map((section) => (
          <Collapse key={section.key} title={section.label} defaultOpen>
            <JsonBlock
              value={(draftObject as Record<string, unknown>)[section.key]}
            />
          </Collapse>
        ))}
        <Collapse title="完整 JSON">
          <JsonBlock value={draftText} />
        </Collapse>
      </div>
      <div className="space-y-1.5 px-1 pt-2">
        <Button
          size="sm"
          variant="outline"
          className="w-full gap-1.5 text-xs"
          onClick={() =>
            navigate("/protocols", {
              state: {
                draft: typeof draft === "string" ? tryParseJSON(draft) : draft,
              },
            })
          }
        >
          <ExternalLink className="h-3.5 w-3.5" /> 在协议设计器中打开
        </Button>
        {canRestore ? (
          <Button
            size="sm"
            variant="outline"
            className="w-full gap-1.5 text-xs"
            title="把配置回滚到最近一轮对话修改前的状态"
            onClick={onRestore}
          >
            <History className="h-3.5 w-3.5" /> 还原到上一轮修改前
          </Button>
        ) : null}
      </div>
    </div>
  );
}
