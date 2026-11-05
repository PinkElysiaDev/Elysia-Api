import { useEffect, useRef, useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import {
  AlertTriangle,
  RefreshCw,
  Save,
} from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { RoleWatermark } from "@/components/role-watermark";
import { TitleTabs } from "@/components/ui/title-tabs";
import { useConfirm } from "@/components/ui/confirm-dialog";
import { Button } from "@/components/ui/button";
import { ErrorState, LoadingState } from "@/components/ui/states";
import { useToast } from "@/components/ui/use-toast";
import { useRuntimeConfigForm } from "./runtime-config/use-runtime-config-form";
import { AgentDefaultsSection } from "./runtime-config/agent-defaults-section";
import { BasicSection } from "./runtime-config/basic-section";
import { SecuritySection } from "./runtime-config/security-section";
import { LogsSection } from "./runtime-config/logs-section";
import { CatalogSection } from "./runtime-config/catalog-section";
import { AgentRemoteSection } from "./runtime-config/agent-remote-section";
import {
  useRuntimeConfig,
  revalidate,
} from "@/lib/hooks";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/utils";

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
  const hasForm = Boolean(form);
  useEffect(() => {
    const top = headerTopRef.current;
    if (!top) return;
    const observer = new IntersectionObserver(([entry]) => setHeaderPinned(entry.boundingClientRect.top < 0));
    observer.observe(top);
    return () => observer.disconnect();
  }, [hasForm]);

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
      void revalidate.usageStorage();
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
          invalid_proxy_url: ["security", "runtime-proxy"],
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
            <BasicSection form={form} update={update} defaultDatabasePath={data?.defaultDatabasePath} />
          </Tabs.Content>
          <Tabs.Content value="security" forceMount className="space-y-8 outline-none data-[state=inactive]:hidden">
            <SecuritySection form={form} update={update} updateOutboundText={updateOutboundText} resetOutboundDefaults={resetOutboundDefaults} />
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
              toolLoopLimit={form.agent.toolLoopLimit}
              onToolLoopLimitChange={(value) => updateAgentDefaults("toolLoopLimit", value)}
            />
          </Tabs.Content>
          <Tabs.Content value="logs" forceMount className="grid items-start gap-8 space-y-8 outline-none data-[state=inactive]:hidden lg:grid-cols-2">
            <LogsSection form={form} update={update} updateUsageLog={updateUsageLog} updateSystemLog={updateSystemLog} toggleUsageBody={toggleUsageBody} />
          </Tabs.Content>
          <Tabs.Content value="catalog" forceMount className="space-y-8 outline-none data-[state=inactive]:hidden">
            <CatalogSection form={form} update={update} />
          </Tabs.Content>
        </fieldset>
      </Tabs.Root>
      {dialog}
    </>
  );
}
