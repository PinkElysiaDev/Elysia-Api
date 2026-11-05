import { AlertTriangle, Layers, RefreshCw } from "lucide-react";
import { useToast } from "@/components/ui/use-toast";
import { useModelCatalogStatus, revalidate } from "@/lib/hooks";
import { useApiAction } from "@/lib/use-api-action";
import { api } from "@/lib/api";
import { formatRelative } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { NumberField } from "@/components/number-field";
import { SettingSection, SettingRow } from "@/components/ui/setting-card";
import type { RuntimeConfigForm, RuntimeConfigActions } from "./use-runtime-config-form";

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

/** Edit the sync interval and refresh the catalog using saved settings. */
export function CatalogSection({ form, update }: Pick<RuntimeConfigActions, "update"> & {
  form: RuntimeConfigForm;
}) {
  const toast = useToast();
  const { run, isBusy } = useApiAction();
  const isRefreshing = isBusy("catalog");
  const { data: catalogStatus } = useModelCatalogStatus();

  async function handleCatalogRefresh() {
    await run("catalog", async () => {
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
    }, { errorTitle: "目录更新失败" });
  }

  return (
    <SettingSection
      icon={Layers}
      title="模型能力目录 (models.dev)"
      description="立即更新使用已保存配置，不会提交当前修改。"
      action={
        <Button
          variant="outline"
          size="sm"
          disabled={
            isRefreshing || form.modelCatalog.enabled === false
          }
          onClick={() => void handleCatalogRefresh()}
        >
          <RefreshCw
            className={
              isRefreshing
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
  );
}
