import { useEffect } from "react";
import { HardDrive, Server, Trash2 } from "lucide-react";
import { LogMaintenanceStatus } from "@/components/log-maintenance-status";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useToast } from "@/components/ui/use-toast";
import { useUsageStorage, useLogMaintenance } from "@/lib/hooks";
import { useApiAction } from "@/lib/use-api-action";
import { api } from "@/lib/api";
import { formatBytes } from "@/lib/utils";
import type { LogLevel } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { NumberField } from "@/components/number-field";
import { SettingSection, SettingRow } from "@/components/ui/setting-card";
import type { RuntimeConfigForm, RuntimeConfigActions } from "./use-runtime-config-form";

/** Edit retention policies and observe maintenance against the saved config. */
export function LogsSection({ form, update, updateUsageLog, updateSystemLog, toggleUsageBody }: Pick<RuntimeConfigActions, "update" | "updateUsageLog" | "updateSystemLog" | "toggleUsageBody"> & {
  form: RuntimeConfigForm;
}) {
  const toast = useToast();
  const { run, isBusy } = useApiAction();
  const isCleaning = isBusy("cleanup");
  const { data: storage, error: storageError, mutate: refreshStorage } = useUsageStorage();
  const { data: maintenance, error: maintenanceError, mutate: refreshMaintenance } = useLogMaintenance();
  useEffect(() => { void refreshStorage(); }, [maintenance?.finishedAt, refreshStorage]);

  async function handleCleanup() {
    await run("cleanup", async () => {
      const result = await api.usageCleanup();
      if (result.accepted) {
        toast.success(
          "清理已触发",
          "后台按留存策略清理并回收空间，可在下方查看进度",
        );
        await refreshMaintenance();
      } else {
        toast.error("维护任务不可用", "服务可能正在关闭");
      }
    }, { errorTitle: "触发清理失败" });
  }

  return (
    <>
      <SettingSection
        icon={HardDrive}
        title="请求日志"
        description="调用日志的留存策略与请求、响应正文保存设置"
      >
        <div className="space-y-1">
          <SettingRow
            label="启用日志持久化"
            htmlFor="runtime-persist"
            description="关闭后新请求完全不落库（统计与日志面板不再更新）"
          >
            <Switch id="runtime-persist" aria-describedby="runtime-persist-description"
              aria-label="启用日志持久化"
              checked={form.usageLog.persistEnabled}
              onCheckedChange={(v) => updateUsageLog("persistEnabled", v)}
            />
          </SettingRow>

          <SettingRow
            label="过期清理天数"
            htmlFor="runtime-retention"
            description="自动删除早于该天数的日志记录（0 = 不启用过期清理）"
          >
            <div className="flex w-full items-center gap-2 sm:w-56">
              <NumberField id="runtime-retention" aria-describedby="runtime-retention-description"
                value={form.usageLog.retentionDays}
                min={0}
                className="font-mono text-xs"
                onCommit={(v) => updateUsageLog("retentionDays", v)}
              />
              <span className="shrink-0 text-xs text-muted-foreground">
                天
              </span>
            </div>
          </SettingRow>

          <SettingRow
            label="请求日志内容预算"
            htmlFor="runtime-budget"
            description="日志 JSON 与去重媒体合计超限时删最旧记录；不含用量汇总、索引及其他业务数据（0 = 不限）"
          >
            <div className="flex w-full items-center gap-2 sm:w-56">
              <NumberField id="runtime-budget" aria-describedby="runtime-budget-description"
                value={form.usageLog.maxContentMB}
                min={0}
                className="font-mono text-xs"
                onCommit={(v) => updateUsageLog("maxContentMB", v)}
              />
              <span className="shrink-0 text-xs text-muted-foreground">
                MB
              </span>
            </div>
          </SettingRow>

          <SettingRow
            label="最大保留条数"
            htmlFor="runtime-records"
            description="超出该条数时自动删除最旧的日志（0 = 不限）"
          >
            <div className="flex w-full items-center gap-2 sm:w-56">
              <NumberField id="runtime-records" aria-describedby="runtime-records-description"
                value={form.usageLog.maxRecords}
                min={0}
                className="font-mono text-xs"
                onCommit={(v) => updateUsageLog("maxRecords", v)}
              />
              <span className="shrink-0 text-xs text-muted-foreground">
                条
              </span>
            </div>
          </SettingRow>

          <SettingRow
            label="保存请求与响应正文"
            htmlFor="runtime-body"
            description={
              <span id="usage-body-description">
                用于排障，可能包含对话内容和上传文件。默认关闭，基础调用统计仍会保留。
                保存后对后续请求生效，历史日志保留。
              </span>
            }
          >
            <Switch id="runtime-body" aria-describedby="runtime-body-description"
              aria-label="保存请求与响应正文"
              checked={form.usageLog.bodyMaxKB > 0}
              onCheckedChange={toggleUsageBody}
            />
          </SettingRow>

          {form.usageLog.bodyMaxKB > 0 && (
            <>
              <SettingRow
                label="正文保存上限"
                htmlFor="runtime-body-limit"
                description="四段链路分别限制保存体积，超出部分截断"
              >
                <div className="flex w-full items-center gap-2 sm:w-56">
                  <NumberField id="runtime-body-limit" aria-describedby="runtime-body-limit-description"
                    aria-label="正文保存上限"
                    value={form.usageLog.bodyMaxKB}
                    min={1}
                    className="font-mono text-xs"
                    onCommit={(v) => updateUsageLog("bodyMaxKB", v)}
                  />
                  <span className="shrink-0 text-xs text-muted-foreground">KB</span>
                </div>
              </SettingRow>
              <SettingRow
                label="仅保存失败请求正文"
                htmlFor="runtime-body-error"
                description="成功请求仅保留元数据，失败请求按正文保存上限记录"
              >
                <Switch id="runtime-body-error" aria-describedby="runtime-body-error-description"
                  aria-label="仅保存失败请求正文"
                  checked={form.usageLog.bodyOnErrorOnly}
                  onCheckedChange={(v) => updateUsageLog("bodyOnErrorOnly", v)}
                />
              </SettingRow>
              <SettingRow
                label="媒体外置保存"
                htmlFor="runtime-body-media"
                description="正文中的 base64 媒体（图片/音频/视频/文件）存为独立文件，正文以占位符替代"
              >
                <Switch id="runtime-body-media" aria-describedby="runtime-body-media-description"
                  aria-label="媒体外置保存"
                  checked={form.usageLog.externalizeMedia}
                  onCheckedChange={(v) => updateUsageLog("externalizeMedia", v)}
                />
              </SettingRow>
            </>
          )}

        </div>
      </SettingSection>
      <SettingSection icon={Server} title="系统日志" description="控制台输出级别与系统日志独立留存策略">
        <div className="space-y-1">
          <SettingRow
            label="日志记录级别"
            htmlFor="runtime-log-level"
            description="控制控制台输出的详细程度，与数据库持久化阈值独立（支持热更新）"
          >
            <div className="w-full sm:w-56">
              <Select
                value={form.logLevel}
                onValueChange={(v) => update("logLevel", v as LogLevel)}
              >
                <SelectTrigger id="runtime-log-level" aria-describedby="runtime-log-level-description" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="debug">Debug（调试）</SelectItem>
                  <SelectItem value="info">Info（信息）</SelectItem>
                  <SelectItem value="warn">Warn（警告）</SelectItem>
                  <SelectItem value="error">Error（错误）</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </SettingRow>


          <SettingRow label="持久化日志级别" htmlFor="runtime-log-persist-level"
            description="诊断日志写入数据库的最低级别；重要操作事件和错误始终尝试落盘">
            <Select value={form.logPersistLevel} onValueChange={(v) => update("logPersistLevel", v as LogLevel)}>
              <SelectTrigger id="runtime-log-persist-level" aria-describedby="runtime-log-persist-level-description" className="w-full sm:w-56"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="debug">Debug（调试）</SelectItem>
                <SelectItem value="info">Info（信息）</SelectItem>
                <SelectItem value="warn">Warn（警告）</SelectItem>
                <SelectItem value="error">Error（错误）</SelectItem>
              </SelectContent>
            </Select>
          </SettingRow>
          <SettingRow label="控制台日志格式" htmlFor="runtime-log-format" description="auto 在终端使用可读格式，重定向和容器环境使用 JSON">
            <Select value={form.logFormat} onValueChange={(v) => update("logFormat", v as "auto" | "json" | "console")}>
              <SelectTrigger id="runtime-log-format" aria-describedby="runtime-log-format-description" className="w-full sm:w-56"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">自动</SelectItem>
                <SelectItem value="json">JSON</SelectItem>
                <SelectItem value="console">可读文本</SelectItem>
              </SelectContent>
            </Select>
          </SettingRow>
          {([
            ['retentionDays', '系统日志保留天数', '天'],
            ['maxRecords', '系统日志保留条数', '条'],
            ['maxContentMB', '系统日志内容预算', 'MiB'],
          ] as const).map(([key, label, unit]) => (
            <SettingRow key={key} htmlFor={`runtime-system-${key}`} label={label} description="0 = 不限；与请求日志独立清理">
              <div className="flex w-full items-center gap-2 sm:w-56">
                <NumberField id={`runtime-system-${key}`} aria-describedby={`runtime-system-${key}-description`} aria-label={label} value={form.systemLog[key]} min={0}
                  className="font-mono text-xs" onCommit={(value) => updateSystemLog(key, value)} />
                <span className="shrink-0 text-xs text-muted-foreground">{unit}</span>
              </div>
            </SettingRow>
          ))}
        </div>
      </SettingSection>
      <SettingSection icon={HardDrive} title="清理与空间占用"
        description="立即清理按已保存的留存策略执行，不会提交当前修改。"
        action={
          <Button
            variant="outline"
            size="sm"
            disabled={isCleaning}
            onClick={() => void handleCleanup()}
          >
            <Trash2 className="h-3.5 w-3.5" /> 立即清理
          </Button>
        }
      >
        <div className="space-y-1">
          <SettingRow
            label="清理巡检周期"
            htmlFor="runtime-cleanup-interval"
            description="后台自动清理的执行周期（分钟，最小 5）"
          >
            <div className="flex w-full items-center gap-2 sm:w-56">
              <NumberField id="runtime-cleanup-interval" aria-describedby="runtime-cleanup-interval-description"
                value={form.usageLog.cleanupIntervalMinutes}
                min={5}
                className="font-mono text-xs"
                onCommit={(v) =>
                  updateUsageLog("cleanupIntervalMinutes", v)
                }
              />
              <span className="shrink-0 text-xs text-muted-foreground">
                分钟
              </span>
            </div>
          </SettingRow>

          <div className="text-xs space-y-3">
            {storageError && <p role="alert" className="text-ember">无法更新存储占用：{storageError.message}</p>}
            <dl className="grid grid-cols-1 gap-4 rounded-xl border border-border/60 bg-card/60 p-4 sm:grid-cols-2">
              {([
                ['日志数据库文件', storage?.db.fileBytes === undefined ? '—' : formatBytes(storage.db.fileBytes)],
                ['媒体目录实际占用（含孤儿文件）', storage?.assets.bytes === undefined ? '—' : formatBytes(storage.assets.bytes)],
                ['请求日志条数', storage?.content.usageRecords.toLocaleString() ?? '—'],
                ['系统日志条数', storage?.content.systemRecords.toLocaleString() ?? '—'],
              ] as const).map(([label, value]) => (
                <div key={label} className="space-y-1">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="text-base font-semibold tabular-nums">{value}</dd>
                </div>
              ))}
            </dl>
            {storage?.writers && <div className="grid gap-3 sm:grid-cols-2">
              {(['system', 'usage'] as const).map((kind) => {
                const writer = storage.writers[kind];
                return <dl key={kind} className="space-y-1 rounded-lg border border-border/60 p-3">
                  <dt className="font-medium">{kind === 'system' ? '系统日志写入' : '调用日志写入'}</dt>
                  <dd>排队 {writer.queued} 条 · 缓冲 {formatBytes(writer.retainedBytes)}</dd>
                  <dd>已提交 {writer.written} · 丢弃 {writer.dropped} · 失败 {writer.failures}</dd>
                  <dd>同步回退 {writer.synchronous} · 最近批次 {writer.lastBatchDurationMs} ms</dd>
                  <dd>最近落盘：{writer.written > 0 ? new Date(writer.lastWriteAt).toLocaleString() : '—'}</dd>
                  {writer.lastError && <dd role="alert" className="break-all text-ember">{writer.lastError}</dd>}
                </dl>;
              })}
            </div>}
            <LogMaintenanceStatus status={maintenance} error={maintenanceError} />
            <details className="group rounded-lg border border-border/60 px-4">
              <summary className="cursor-pointer py-3 font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">查看占用明细</summary>
              <div className="space-y-2 pb-4">
                {([

                  ['有效数据页（含索引）', storage?.db.usedBytes],
                  ['可回收空闲页', storage?.db.freeBytes],
                  ['WAL 文件', storage?.db.walBytes],
                  ['请求日志 JSON', storage?.content.usageBytes],
                  ['引用中的去重媒体', storage?.content.mediaBytes],
                  ['待删除媒体', storage?.content.pendingMediaBytes],
                  ['系统日志内容', storage?.content.systemBytes],
                  ['长期保存的用量汇总', storage?.db.rollupBytes],
                  ['数据库索引', storage?.db.indexBytes],
                  ['页结构与页内空隙（含索引页，不与上项相加）', storage?.db.pageOverheadBytes],
                ] as const).map(([label, bytes]) => (
                  <div key={label} className="flex items-center justify-between text-muted-foreground gap-3">
                    <span>{label}</span><span className="font-semibold text-foreground">{bytes === undefined ? '—' : formatBytes(bytes)}</span>
                  </div>
                ))}
              </div>
            </details>
            <p className="pt-1 text-2xs text-muted-foreground/70">
              内容预算不是磁盘硬上限。历史用量汇总长期保留，只有重置用量才删除；即使留存不限，也会回收已删除数据的空闲页。升级备份不自动删除、不计入上述占用。
            </p>
          </div>
        </div>
      </SettingSection>
    </>
  );
}
