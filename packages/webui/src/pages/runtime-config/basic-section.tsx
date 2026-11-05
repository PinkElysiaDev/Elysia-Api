import { Database, RotateCcw, Server } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NumberField } from "@/components/number-field";
import { SettingSection, SettingRow } from "@/components/ui/setting-card";
import type { RuntimeConfigForm, RuntimeConfigActions } from "./use-runtime-config-form";

/** Edit service and storage fields within the shared unsaved form. */
export function BasicSection({ form, update, defaultDatabasePath }: Pick<RuntimeConfigActions, "update"> & {
  form: RuntimeConfigForm;
  defaultDatabasePath?: string;
}) {
  return (
    <>
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
        description="业务与日志使用独立 SQLite 数据库"
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
                  placeholder={defaultDatabasePath}
                  onChange={(e) => update("databasePath", e.target.value)}
                />
                <Button
                  variant="outline"
                  size="iconSm"
                  title="恢复为系统默认路径"
                  aria-label="恢复为系统默认路径"
                  onClick={() =>
                    update("databasePath", defaultDatabasePath ?? "")
                  }
                >
                  <RotateCcw className="h-3.5 w-3.5" />
                </Button>
              </div>
              {defaultDatabasePath &&
                form.databasePath !== defaultDatabasePath && (
                  <p className="text-2xs text-muted-foreground">
                    系统默认路径：
                    <code className="break-all rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">
                      {defaultDatabasePath}
                    </code>
                  </p>
                )}
            </div>
          </SettingRow>
          <SettingRow label="日志数据库路径" htmlFor="runtime-log-database"
            description="系统日志、调用明细和用量汇总的专用数据库；不能与业务库相同。变更需重启，不迁移历史数据。" inline={false}>
            <Input id="runtime-log-database" aria-describedby="runtime-log-database-description"
              className="font-mono text-xs" value={form.logDatabasePath}
              onChange={(e) => update("logDatabasePath", e.target.value)} />
          </SettingRow>
        </div>
      </SettingSection>
    </>
  );
}
