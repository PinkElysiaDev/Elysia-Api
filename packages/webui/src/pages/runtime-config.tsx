import { useEffect, useRef, useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import {
  AlertTriangle,
  Database,
  Eye,
  EyeOff,
  HardDrive,
  Layers,
  RefreshCw,
  RotateCcw,
  Save,
  Server,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { LogMaintenanceStatus } from "@/components/log-maintenance-status";
import { PageHeader } from "@/components/page-header";
import { RoleWatermark } from "@/components/role-watermark";
import { TitleTabs } from "@/components/ui/title-tabs";
import { useConfirm } from "@/components/ui/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NumberField } from "@/components/number-field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { SettingSection, SettingRow } from "@/components/ui/setting-card";
import { ErrorState, LoadingState } from "@/components/ui/states";
import { useToast } from "@/components/ui/use-toast";
import { useRuntimeConfigForm } from "./runtime-config/use-runtime-config-form";
import { AgentDefaultsSection } from "./runtime-config/agent-defaults-section";
import { AgentRemoteSection } from "./runtime-config/agent-remote-section";
import {
  useUsageStorage,
  useLogMaintenance,
  useRuntimeConfig,
  useModelCatalogStatus,
  revalidate,
} from "@/lib/hooks";
import { api, ApiError } from "@/lib/api";
import { formatRelative, formatBytes, cn } from "@/lib/utils";
import type { LogLevel } from "@/lib/types";

// 目录数据来源的展示名。
function catalogSourceLabel(source: string): string {
  switch (source) {
    case "snapshot":
      return "内置快照";
    case "cache":
      return "本地缓存";
    case "network":
      return "在线更新";
    default:
      return source;
  }
}

const configTabs = [
  ["basic", "基础设置"],
  ["security", "安全设置"],
  ["remote", "远程访问"],
  ["agent", "AI 助手"],
  ["logs", "日志与存储"],
  ["catalog", "模型目录"],
] as const;
type ConfigTab = typeof configTabs[number][0];

export function RuntimeConfigPage() {
  const toast = useToast();
  const { confirm, dialog } = useConfirm();
  const [activeTab, setActiveTab] = useState<ConfigTab>("basic");
  const headerTopRef = useRef<HTMLDivElement>(null);
  const [headerPinned, setHeaderPinned] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [pendingFocus, setPendingFocus] = useState<string | null>(null);
  const { data, isLoading, error, mutate } = useRuntimeConfig();
  const { data: catalogStatus } = useModelCatalogStatus();
  const {
    form,
    update,
    updateUsageLog,
    updateSystemLog,
    toggleUsageBody,
    updateOutboundText,
    updateAgentRemote,
    updateAgentDefaults,
    resetOutboundDefaults,
    dirtyBlockPayload,
    isDirty,
    reset,
  } = useRuntimeConfigForm(data);
  const [saving, setSaving] = useState(false);
  const [restartNotice, setRestartNotice] = useState(false);
  const [showToken, setShowToken] = useState(false);
  const [catalogRefreshing, setCatalogRefreshing] = useState(false);
  const { data: storage, error: storageError, mutate: refreshStorage } = useUsageStorage();
  const { data: maintenance, error: maintenanceError, mutate: refreshMaintenance } = useLogMaintenance();
  const [cleaning, setCleaning] = useState(false);
  useEffect(() => { void refreshStorage(); }, [maintenance?.finishedAt, refreshStorage]);

  const hasForm = Boolean(form);
  useEffect(() => {
    const top = headerTopRef.current;
    if (!top) return;
    const observer = new IntersectionObserver(([entry]) => setHeaderPinned(entry.boundingClientRect.top < 0));
    observer.observe(top);
    return () => observer.disconnect();
  }, [hasForm]);

  async function handleCatalogRefresh() {
    setCatalogRefreshing(true);
    try {
      const result = await api.modelCatalogRefresh();
      await revalidate.modelCatalogStatus();
      const status = result.status;
      if (status.lastError) {
        toast.error("目录更新失败", status.lastError);
      } else {
        toast.success(
          "能力目录已更新",
          `已加载 ${status.entries} 个模型${status.lastSync ? ` · ${formatRelative(status.lastSync)}` : ""}`,
        );
      }
    } catch (err) {
      toast.error("目录更新失败", (err as Error).message);
    } finally {
      setCatalogRefreshing(false);
    }
  }

  async function handleCleanup() {
    setCleaning(true);
    try {
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
    } catch (err) {
      toast.error("触发清理失败", (err as Error).message);
    } finally {
      setCleaning(false);
    }
  }

  function changeTab(tab: ConfigTab) {
    setActiveTab(tab);
    // 长分类滚动后切换短分类时，从表单顶部开始阅读。
    if (window.scrollY > 0) {
      window.scrollTo({ top: 0, behavior: "auto" });
    }
  }

  function focusField(tab: ConfigTab, id: string) {
    changeTab(tab);
    setPendingFocus(id);
  }

  // 等待面板显示和 fieldset 解锁后再聚焦，避免 WebKit 忽略禁用期间的 focus。
  useEffect(() => {
    if (!pendingFocus || saving || reloading) return;
    const frame = requestAnimationFrame(() => {
      const field = document.getElementById(pendingFocus);
      field?.focus({ preventScroll: true });
      field?.scrollIntoView({ block: "center" });
      setPendingFocus(null);
    });
    return () => cancelAnimationFrame(frame);
  }, [pendingFocus, saving, reloading]);

  async function handleReload() {
    if (saving || reloading) return;
    if (isDirty && !await confirm({
      title: "丢弃修改并重载配置？",
      description: "所有分类中未保存的修改都会丢失，并重新读取服务端配置。",
      confirmText: "丢弃并重载",
    })) return;
    setReloading(true);
    try {
      await api.reload();
      const saved = await mutate();
      if (saved) reset(saved);
      toast.success("已触发配置热重载");
    } catch (err) {
      toast.error("热重载失败", (err as Error).message);
    } finally {
      setReloading(false);
    }
  }

  async function handleSave() {
    if (!form || saving || reloading) return;
    // 下界由 NumberField 的 min 钳制保证，这里只校验上界。
    if (form.port > 65535) {
      focusField("basic", "runtime-port");
      toast.error("端口非法", "port 不能超过 65535");
      return;
    }
    // 禁止段前端先逐条校验，非法条目直接拦截（后端同款校验兜底）。
    const invalidEntries = form.outbound.deniedIpRanges.filter((entry) => {
      const trimmed = entry.trim();
      return trimmed !== "" && !/^[0-9a-fA-F:.]+\/\d{1,3}$/.test(trimmed);
    });
    if (invalidEntries.length > 0) {
      focusField("security", "runtime-outbound");
      toast.error(
        "禁止出站 IP 段格式非法",
        `这些条目不是合法 CIDR：${invalidEntries.join("、")}`,
      );
      return;
    }
    setSaving(true);
    try {
      const result = await api.updateRuntimeConfig({
        host: form.host,
        port: form.port,
        logLevel: form.logLevel,
        httpTimeout: form.httpTimeout,
        // 留空 = 不修改现有令牌；提交空串会被后端拒绝（空令牌会锁死面板）。
        panelAccessToken: form.panelAccessToken.trim()
          ? form.panelAccessToken
          : undefined,
        databasePath: form.databasePath,
        enablePprof: form.enablePprof,
        // 块级脏检查：未改过的块不发送，避免用旧快照覆盖并发改动。
        ...dirtyBlockPayload(),
      });
      const saved = await mutate();
      if (saved) reset(saved);
      refreshStorage();
      setRestartNotice(result.restartRequired);
      toast.success(
        "运行配置已更新",
        result.restartRequired
          ? "部分变更需重启后端生效"
          : "热更新字段已即时生效",
      );
    } catch (err) {
      if (err instanceof ApiError) {
        const fields: Record<string, [ConfigTab, string]> = {
          invalid_host: ["basic", "runtime-host"],
          invalid_port: ["basic", "runtime-port"],
          invalid_http_timeout: ["basic", "runtime-timeout"],
          invalid_database_path: ["basic", "runtime-database"],
          invalid_panel_access_token: ["security", "runtime-token"],
          invalid_outbound: ["security", "runtime-outbound"],
          invalid_public_url: ["remote", "runtime-public-url"],
          invalid_sync_interval: ["catalog", "runtime-catalog-interval"],
          invalid_log_level: ["logs", "runtime-log-level"],
        };
        const field = fields[err.code];
        if (field) focusField(...field);
      }
      toast.error("保存失败", (err as Error).message);
    } finally {
      setSaving(false);
    }
  }

  if (isLoading && !form) {
    return (
      <div className="space-y-6">
        <PageHeader title="运行配置" />
        <LoadingState rows={4} columns={2} />
      </div>
    );
  }

  if (error && !form) {
    return (
      <div className="space-y-6">
        <PageHeader title="运行配置" />
        <ErrorState
          message={(error as Error).message}
          onRetry={() => mutate()}
        />
      </div>
    );
  }

  if (!form) return null;

  return (
    <>
      <RoleWatermark className="right-0 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <Tabs.Root value={activeTab} onValueChange={(value) => changeTab(value as ConfigTab)} className="relative z-[1]">
        <PageHeader title="运行配置" />
        <div ref={headerTopRef} aria-hidden className="h-px -mb-px" />
        <div role="region" aria-label="运行配置工具栏" className={cn(
          "sticky top-0 z-30 -mx-6 flex min-h-14 items-center gap-6 border-b border-border/40 px-6 max-rail:-mx-4 max-rail:gap-3 max-rail:px-4",
          headerPinned && "bg-background/65 backdrop-blur-xl backdrop-saturate-150 max-rail:pl-[72px]",
        )}>
          <div className="min-w-0 flex-1">
            <TitleTabs aria-label="配置分类" value={activeTab} options={configTabs.map(([value, label]) => ({ value, label }))} />
          </div>
          <div className="flex shrink-0 items-center gap-2.5">
            <Button variant="ghost" onClick={() => void handleReload()} disabled={saving || reloading}>
              <RefreshCw className="h-4 w-4" />
              <span className="max-rail:sr-only">{reloading ? "重载中…" : "重载配置"}</span>
            </Button>
            {/* 默认白瓷键，有未保存修改时切换梅釉主色提醒保存 */}
            <Button variant={isDirty ? "primary" : "default"} onClick={() => void handleSave()} disabled={saving || reloading}>
              <Save className="h-4 w-4" />
              <span className="max-rail:sr-only">{saving ? "保存中…" : isDirty ? "保存修改" : "保存配置"}</span>
            </Button>
          </div>
        </div>

        {error && <p role="alert" className="mt-4 text-sm text-ember">无法刷新配置，当前输入已保留：{(error as Error).message}</p>}
        {restartNotice && (
          <div className="mt-4 flex items-center gap-3 rounded-xl border border-[color:color-mix(in_srgb,var(--amber)_40%,transparent)] bg-[color-mix(in_srgb,var(--amber)_10%,transparent)] px-4 py-3 text-sm text-amber shadow-sm">
            <AlertTriangle className="h-5 w-5 shrink-0" />
            <span>
              部分基础配置已变更，需要手动重启或通过服务管理器重启后端进程方可生效。
            </span>
          </div>
        )}

        <fieldset disabled={saving || reloading} aria-label="运行配置表单" aria-busy={saving || reloading} className="min-w-0 w-full pt-6 disabled:opacity-70">
          <Tabs.Content value="basic" forceMount className="grid items-start gap-8 space-y-8 outline-none data-[state=inactive]:hidden lg:grid-cols-2">
            <SettingSection
              icon={Server}
              title="服务与网络"
              description="网关监听地址、网络端口与核心超时设置"
            >
              <div className="space-y-1">
                <SettingRow
                  label="监听 Host"
                  htmlFor="runtime-host"
                  description="网关绑定的网络接口（如 127.0.0.1 或 0.0.0.0）；需重启生效"
                >
                  <Input id="runtime-host" aria-describedby="runtime-host-description"
                    className="w-full sm:w-56 font-mono text-xs"
                    value={form.host}
                    placeholder="127.0.0.1"
                    onChange={(e) => update("host", e.target.value)}
                  />
                </SettingRow>

                <SettingRow
                  label="监听 Port"
                  htmlFor="runtime-port"
                  description="服务监听端口（1 ~ 65535，需重启生效）"
                >
                  <NumberField id="runtime-port" aria-describedby="runtime-port-description"
                    value={form.port}
                    min={1}
                    className="w-full sm:w-56 font-mono text-xs"
                    onCommit={(v) => update("port", v)}
                  />
                </SettingRow>

                <SettingRow
                  label="HTTP 超时时间"
                  htmlFor="runtime-timeout"
                  description="上游请求超时时间（秒，0 表示不设硬性超时）"
                >
                  <div className="flex w-full items-center gap-2 sm:w-56">
                    <NumberField id="runtime-timeout" aria-describedby="runtime-timeout-description"
                      value={form.httpTimeout}
                      min={0}
                      className="font-mono text-xs"
                      onCommit={(v) => update("httpTimeout", v)}
                    />
                    <span className="shrink-0 text-xs text-muted-foreground">
                      秒
                    </span>
                  </div>
                </SettingRow>
              </div>
            </SettingSection>
            <SettingSection
              icon={Database}
              title="持久化存储"
              description="SQLite 数据库文件路径与用量记录存储"
            >
              <div className="space-y-1">
                <SettingRow
                  label="数据库存储路径"
                  htmlFor="runtime-database"
                  description="SQLite 数据库文件存放路径（变更需重启生效）"
                  inline={false}
                >
                  <div className="space-y-2">
                    <div className="flex items-center gap-2">
                      <Input id="runtime-database" aria-describedby="runtime-database-description"
                        className="font-mono text-xs"
                        value={form.databasePath}
                        placeholder={data?.defaultDatabasePath}
                        onChange={(e) => update("databasePath", e.target.value)}
                      />
                      <Button
                        variant="outline"
                        size="iconSm"
                        title="恢复为系统默认路径"
                        aria-label="恢复为系统默认路径"
                        onClick={() =>
                          update("databasePath", data?.defaultDatabasePath ?? "")
                        }
                      >
                        <RotateCcw className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                    {data?.defaultDatabasePath &&
                      form.databasePath !== data.defaultDatabasePath && (
                        <p className="text-2xs text-muted-foreground">
                          系统默认路径：
                          <code className="break-all rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">
                            {data.defaultDatabasePath}
                          </code>
                        </p>
                      )}
                  </div>
                </SettingRow>
              </div>
            </SettingSection>

          </Tabs.Content>
          <Tabs.Content value="security" forceMount className="space-y-8 outline-none data-[state=inactive]:hidden">
            <SettingSection
              icon={ShieldCheck}
              title="安全与访问鉴权"
              description="WebUI 面板口令与网络出站安全守卫"
            >
              <div className="space-y-1">
                <SettingRow
                  label="Panel Access Token"
                  htmlFor="runtime-token"
                  description="用于登录控制台与管理 API 的鉴权令牌；留空提交则保持原值"
                >
                  <div className="flex w-full sm:w-72 items-center gap-1.5">
                    <Input id="runtime-token" aria-describedby="runtime-token-description"
                      type={showToken ? "text" : "password"}
                      value={form.panelAccessToken}
                      placeholder="输入新令牌（留空不变）"
                      className="font-mono text-xs"
                      onChange={(e) => update("panelAccessToken", e.target.value)}
                    />
                    <Button
                      variant="outline"
                      size="iconSm"
                      type="button"
                      title={showToken ? "隐藏明文" : "显示明文"}
                      aria-label={showToken ? "隐藏访问令牌明文" : "显示访问令牌明文"}
                      onClick={() => setShowToken(!showToken)}
                    >
                      {showToken ? (
                        <EyeOff className="h-3.5 w-3.5" />
                      ) : (
                        <Eye className="h-3.5 w-3.5" />
                      )}
                    </Button>
                  </div>
                </SettingRow>

                <SettingRow
                  label="禁止出站 IP 段（CIDR）"
                  htmlFor="runtime-outbound"
                  description="SSRF 防护的拨号黑名单，一行一段。默认预置私网/环回/保留段；上游是本机服务（如 127.0.0.1）被拦截时，删除对应段放行（如 127.0.0.0/8）"
                  inline={false}
                >
                  <div className="w-full space-y-2">
                    <textarea id="runtime-outbound" aria-describedby="runtime-outbound-description"
                      className="min-h-[120px] w-full rounded-md border border-border bg-card px-3 py-2 font-mono text-xs outline-none transition-colors focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash"
                      spellCheck={false}
                      value={form.outbound.deniedIpRanges.join("\n")}
                      onChange={(e) => updateOutboundText(e.target.value)}
                    />
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        type="button"
                        onClick={resetOutboundDefaults}
                      >
                        <RotateCcw className="mr-1.5 h-3.5 w-3.5" />
                        恢复默认
                      </Button>
                      <span className="text-2xs text-soft">
                        当前{" "}
                        {
                          form.outbound.deniedIpRanges.filter(
                            (entry) => entry.trim() !== "",
                          ).length
                        }{" "}
                        段
                        {form.outbound.deniedIpRanges.join("\n") !==
                          (form.outbound.defaultDeniedIpRanges ?? []).join(
                            "\n",
                          ) && " · 与默认不同"}
                      </span>
                    </div>
                    {form.outbound.deniedIpRanges.every(
                      (entry) => entry.trim() === "",
                    ) && (
                        <div className="rounded-lg bg-[color-mix(in_srgb,var(--ember)_10%,transparent)] p-2.5 text-2xs text-ember flex items-center gap-2">
                          <AlertTriangle className="h-4 w-4 shrink-0" />
                          <span>
                            列表为空 =
                            放行所有出站地址（含环回、私网与云元数据端点），仅建议完全可信的内网环境使用。
                          </span>
                        </div>
                      )}
                  </div>
                </SettingRow>
              </div>
            </SettingSection>

          </Tabs.Content>
          <Tabs.Content value="remote" forceMount className="space-y-8 outline-none data-[state=inactive]:hidden">
            <AgentRemoteSection
              form={form}
              onToggle={(enabled) => updateAgentRemote("enabled", enabled)}
              onPublicUrlChange={(url) => updateAgentRemote("publicUrl", url)}
            />

          </Tabs.Content>
          <Tabs.Content value="agent" forceMount className="space-y-8 outline-none data-[state=inactive]:hidden">
            <AgentDefaultsSection
              toolLoopLimit={form?.agent.toolLoopLimit ?? 30}
              onToolLoopLimitChange={(value) => updateAgentDefaults("toolLoopLimit", value)}
            />
          </Tabs.Content>
          <Tabs.Content value="logs" forceMount className="grid items-start gap-8 space-y-8 outline-none data-[state=inactive]:hidden lg:grid-cols-2">
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
                  description="控制控制台与文件日志输出的详细程度（支持热更新）"
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
                  disabled={cleaning}
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
                      ['数据库文件', storage?.db.fileBytes === undefined ? '—' : formatBytes(storage.db.fileBytes)],
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
          </Tabs.Content>
          <Tabs.Content value="catalog" forceMount className="space-y-8 outline-none data-[state=inactive]:hidden">
            <SettingSection
              icon={Layers}
              title="模型能力目录 (models.dev)"
              description="立即更新使用已保存配置，不会提交当前修改。"
              action={
                <Button
                  variant="outline"
                  size="sm"
                  disabled={
                    catalogRefreshing || form.modelCatalog.enabled === false
                  }
                  onClick={() => void handleCatalogRefresh()}
                >
                  <RefreshCw
                    className={
                      catalogRefreshing
                        ? "h-3.5 w-3.5 animate-spin"
                        : "h-3.5 w-3.5"
                    }
                  />{" "}
                  立即更新
                </Button>
              }
            >
              <div className="space-y-1">
                <SettingRow
                  label="后台自动同步周期"
                  htmlFor="runtime-catalog-interval"
                  description="定期后台同步周期（分钟，0 表示不启用该功能）"
                >
                  <div className="flex w-full items-center gap-2 sm:w-56">
                    <NumberField id="runtime-catalog-interval" aria-describedby="runtime-catalog-interval-description"
                      value={form.modelCatalog.syncIntervalMinutes}
                      min={0}
                      className="font-mono text-xs"
                      onCommit={(v) =>
                        update("modelCatalog", {
                          ...form.modelCatalog,
                          syncIntervalMinutes: v,
                        })
                      }
                    />
                    <span className="shrink-0 text-xs text-muted-foreground">
                      分钟
                    </span>
                  </div>
                </SettingRow>

                <div className="border-t border-border/40 pt-3 text-xs space-y-2">
                  <div className="flex items-center justify-between text-muted-foreground">
                    <span>当前加载规模</span>
                    <span className="font-semibold text-foreground">
                      {catalogStatus && catalogStatus.entries > 0
                        ? `${catalogStatus.entries} 个模型规范`
                        : "未加载"}
                    </span>
                  </div>
                  <div className="flex items-center justify-between text-muted-foreground">
                    <span>数据来源</span>
                    <span className="font-medium text-foreground">
                      {catalogSourceLabel(catalogStatus?.source ?? "snapshot")}
                    </span>
                  </div>
                  {catalogStatus?.lastSync && (
                    <div className="flex items-center justify-between text-muted-foreground">
                      <span>最近更新时间</span>
                      <span className="font-mono text-2xs">
                        {formatRelative(catalogStatus.lastSync)}
                      </span>
                    </div>
                  )}
                  {catalogStatus?.lastError && (
                    <p className="mt-2 text-2xs text-ember flex items-center gap-1">
                      <AlertTriangle className="h-3 w-3 shrink-0" />{" "}
                      在线拉取异常：{catalogStatus.lastError}
                    </p>
                  )}
                </div>
              </div>
            </SettingSection>

          </Tabs.Content>
        </fieldset>
      </Tabs.Root>
      {dialog}
    </>
  );
}
