import { Bot, Keyboard } from "lucide-react"

import { NumberField } from "@/components/number-field"
import { SettingRow, SettingSection } from "@/components/ui/setting-card"
import { cn } from "@/lib/utils"
import {
  setEscActionMode,
  setSendKeyMode,
  useEscActionMode,
  useSendKeyMode,
} from "@/pages/agent/send-key"

/** AI 助手全局默认与快捷键偏好（自 AI 助手首页迁入）。 */

function ShortcutOption({
  selected,
  label,
  hint,
  onClick,
}: {
  selected: boolean
  label: string
  hint?: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onClick}
      className="flex w-full items-start gap-2 rounded-lg px-2.5 py-1.5 text-left transition-colors hover:bg-wash"
    >
      <span
        aria-hidden
        className={cn(
          "mt-1 h-1.5 w-1.5 shrink-0 rounded-full",
          selected ? "bg-rose" : "bg-transparent",
        )}
      />
      <span className="min-w-0">
        <span className={cn("block text-xs leading-5", selected && "font-medium text-rose")}>{label}</span>
        {hint ? <span className="block text-2xs leading-4 text-muted-foreground">{hint}</span> : null}
      </span>
    </button>
  )
}

export function AgentDefaultsSection({
  toolLoopLimit,
  onToolLoopLimitChange,
}: {
  toolLoopLimit: number
  onToolLoopLimitChange: (value: number) => void
}) {
  const sendMode = useSendKeyMode()
  const escMode = useEscActionMode()

  return (
    <>
      <SettingSection icon={Bot} title="工具循环上限" description="AI 助手单轮「模型调用 → 工具 → 回传」的循环上限（全局默认，保存即热生效）">
        <div className="space-y-1">
          <SettingRow
            label="每轮工具循环上限"
            htmlFor="runtime-tool-loop"
            description="一批命令算 1 轮；0 = 默认 30，范围 0-100。达到上限后轮次正常收尾，继续对话即开始新一轮"
          >
            <div className="flex w-full items-center gap-2 sm:w-56">
              <NumberField
                id="runtime-tool-loop"
                value={toolLoopLimit}
                min={0}
                className="font-mono text-xs"
                onCommit={(value: number) => onToolLoopLimitChange(Math.max(0, Math.min(100, Math.round(value))))}
              />
              <span className="shrink-0 text-xs text-muted-foreground">次 / 轮</span>
            </div>
          </SettingRow>
        </div>
      </SettingSection>
      <SettingSection icon={Keyboard} title="快捷键" description="发送与审批卡快捷键偏好（本浏览器全局生效，所有 AI 助手会话即时应用）">
        <div className="space-y-4">
          <div role="radiogroup" aria-label="发送键">
            <p className="px-2.5 pb-0.5 text-2xs text-muted-foreground">发送键</p>
            <ShortcutOption
              selected={sendMode === "enter"}
              label="Enter 发送"
              hint="Enter 发送消息，Shift+Enter 换行（输入法组词回车不触发）"
              onClick={() => setSendKeyMode("enter")}
            />
            <ShortcutOption
              selected={sendMode === "ctrl-enter"}
              label="Ctrl+Enter 发送"
              hint="Ctrl/Cmd+Enter 发送消息，Enter 换行"
              onClick={() => setSendKeyMode("ctrl-enter")}
            />
          </div>
          <div role="radiogroup" aria-label="审批卡快捷键">
            <p className="px-2.5 pb-0.5 text-2xs text-muted-foreground">审批卡快捷键</p>
            <ShortcutOption
              selected={escMode === "on"}
              label="Esc 拒绝 / 跳过"
              hint="审批卡 Esc=拒绝，提问卡 Esc=跳过作答（输入中有内容时不触发）"
              onClick={() => setEscActionMode("on")}
            />
            <ShortcutOption
              selected={escMode === "off"}
              label="关闭 Esc 快捷键"
              hint="Esc 不触发卡片动作"
              onClick={() => setEscActionMode("off")}
            />
          </div>
        </div>
      </SettingSection>
    </>
  )
}
