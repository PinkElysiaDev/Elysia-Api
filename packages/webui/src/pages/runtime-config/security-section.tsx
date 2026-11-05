import { useState } from "react";
import { AlertTriangle, Eye, EyeOff, RotateCcw, ShieldCheck } from "lucide-react";
import { SecretInput } from "@/components/secret-input";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingSection, SettingRow } from "@/components/ui/setting-card";
import type { RuntimeConfigForm, RuntimeConfigActions } from "./use-runtime-config-form";

/** Keep credential visibility local while edits remain in the shared form. */
export function SecuritySection({ form, update, updateOutboundText, resetOutboundDefaults }: Pick<RuntimeConfigActions, "update" | "updateOutboundText" | "resetOutboundDefaults"> & {
  form: RuntimeConfigForm;
}) {
  const [isTokenVisible, setTokenVisible] = useState(false);
  return (
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
              type={isTokenVisible ? "text" : "password"}
              value={form.panelAccessToken}
              placeholder="输入新令牌（留空不变）"
              className="font-mono text-xs"
              onChange={(e) => update("panelAccessToken", e.target.value)}
            />
            <Button
              variant="outline"
              size="iconSm"
              type="button"
              title={isTokenVisible ? "隐藏明文" : "显示明文"}
              aria-label={isTokenVisible ? "隐藏访问令牌明文" : "显示访问令牌明文"}
              onClick={() => setTokenVisible(!isTokenVisible)}
            >
              {isTokenVisible ? (
                <EyeOff className="h-3.5 w-3.5" />
              ) : (
                <Eye className="h-3.5 w-3.5" />
              )}
            </Button>
          </div>
        </SettingRow>

        <SettingRow
          label="出站代理 URL（可选）"
          htmlFor="runtime-proxy"
          description="填写 HTTP 代理地址，支持用户名和密码，可转发 HTTPS 请求。用于模型请求、目录同步及 macOS 更新；本机面板保持直连。留空保持默认连接方式，代理不可用时不会改为直连。"
          inline={false}
        >
          <SecretInput id="runtime-proxy" aria-describedby="runtime-proxy-description"
            className="font-mono text-xs" autoComplete="off" spellCheck={false}
            placeholder="http://127.0.0.1:7890" value={form.outbound.proxyUrl ?? ""}
            onChange={(event) => update("outbound", { ...form.outbound, proxyUrl: event.target.value })} />
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
  );
}
