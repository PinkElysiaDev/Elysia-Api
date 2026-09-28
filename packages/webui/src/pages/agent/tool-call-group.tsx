import { Collapse } from "./ui-blocks";
import { ToolCallRow, type ToolCallEntry } from "./tool-call-row";

/** 连续调用共用一个入口；结果落库使用同一 callId，保留展开状态。 */
export function ToolCallGroup({ calls }: { calls: ToolCallEntry[] }) {
  const running = calls.filter((call) => call.status === "running").length;
  const failed = calls.filter((call) => call.status === "failed" || call.status === "denied").length;
  return (
    <Collapse
      title="调用工具"
      summary={
        <span className="flex items-center gap-2 text-2xs">
          <span className="tnum">{calls.length} 次</span>
          {running > 0 ? <span>执行中</span> : null}
          {failed > 0 ? <span className="text-ember">{failed} 项未成功</span> : null}
        </span>
      }
    >
      <div className="min-w-0 space-y-0.5">
        {calls.map((call) => <ToolCallRow key={call.id} call={call} />)}
      </div>
    </Collapse>
  );
}
