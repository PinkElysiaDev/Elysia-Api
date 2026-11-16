import { ExternalLink, History } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapse, JsonBlock } from "./ui-blocks";
import type { AgentSession } from "@/lib/agent/types";
import { parseProtocolPackage } from "@/lib/protocol-v2";
import type { ProtocolDocument } from "@/lib/protocol-document";

const DRAFT_SECTIONS: { key: string; label: string }[] = [
  { key: "spec", label: "外部协议 spec.json" },
  { key: "mapping", label: "核心映射 mapping.json" },
];

/** Show the exact package sources shared by Agent authoring and the designer. */
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
  const draftText = session.definitionJSON ?? (
    draft == null
      ? ""
      : typeof draft === "string"
        ? draft
        : JSON.stringify(draft, null, 2));
  let document: ProtocolDocument | undefined;
  try {
    document = parseProtocolPackage(draftText);
  } catch {
    // Agent drafts can be incomplete; keep their exact text visible for repair.
  }
  const readIdentity = (key: string) => {
    const raw = document?.read(`/spec/${key}`);
    const value: unknown = raw === undefined ? undefined : JSON.parse(raw);
    return typeof value === "string" ? value : "";
  };
  const protocolId = readIdentity("id");
  const protocolName = readIdentity("name");
  const restoreText = session.restoreJSON ?? (
    session.draftRestore == null ? "" : JSON.stringify(session.draftRestore));
  const canRestore =
    restoreText !== "" &&
    draftText !== "" &&
    restoreText !== draftText &&
    !busy;

  if (!draftText) return null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-w-0 items-center gap-2 px-1 py-1.5">
        {protocolId ? (
          <Badge variant="outline" className="max-w-full shrink-0 truncate font-mono text-2xs">
            {protocolId}
          </Badge>
        ) : null}
        {protocolName ? (
          <span className="min-w-0 truncate text-xs text-muted-foreground">
            {protocolName}
          </span>
        ) : null}
      </div>
      <div className="no-scrollbar min-h-0 flex-1 space-y-2 overflow-y-auto overscroll-contain px-1 pb-4">
        {document && DRAFT_SECTIONS.map((section) => (
          <Collapse key={section.key} title={section.label}>
            <JsonBlock
              value={document.read(`/${section.key}`)}
            />
          </Collapse>
        ))}
        <Collapse title="完整 JSON">
          <JsonBlock value={draftText} />
        </Collapse>
      </div>
      <div className="space-y-1.5 border-t border-border/50 px-1 pt-3">
        <Button
          size="sm"
          variant="primary"
          className="w-full gap-1.5 text-xs focus-visible:ring-0 focus-visible:ring-offset-0 focus-visible:brightness-110"
          onClick={() =>
            navigate("/protocols", {
              state: {
                definitionJSON: draftText,
              },
            })
          }
        >
          <ExternalLink className="h-3.5 w-3.5" /> 在协议设计器中打开
        </Button>
        {canRestore ? (
          <Button
            size="sm"
            variant="ghost"
            className="w-full gap-1.5 text-xs focus-visible:ring-0 focus-visible:ring-offset-0 focus-visible:brightness-110"
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
