import { Check, Keyboard } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import {
  setEscActionMode,
  setSendKeyMode,
  useEscActionMode,
  useSendKeyMode,
} from "./send-key";

/** 快捷键设置二级窗口：入口在 AI 助手首页标题右侧，偏好全局生效。 */

function OptionRow({
  selected,
  label,
  hint,
  onClick,
}: {
  selected: boolean;
  label: string;
  hint?: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onClick}
      className="flex w-full items-start gap-2 rounded-lg px-2.5 py-1.5 text-left transition-colors hover:bg-wash"
    >
      <Check
        className={cn(
          "mt-0.5 h-3.5 w-3.5 shrink-0 text-rose",
          !selected && "opacity-0",
        )}
      />
      <span className="min-w-0">
        <span
          className={cn(
            "block text-xs leading-5",
            selected && "font-medium text-rose",
          )}
        >
          {label}
        </span>
        {hint ? (
          <span className="block text-2xs leading-4 text-muted-foreground">
            {hint}
          </span>
        ) : null}
      </span>
    </button>
  );
}

export function ShortcutSettingsDialog() {
  const sendMode = useSendKeyMode();
  const escMode = useEscActionMode();

  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="ghost">
          <Keyboard className="h-4 w-4" /> 快捷键设置
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>快捷键设置</DialogTitle>
          <DialogDescription>
            全局偏好，所有 AI 助手会话即时生效。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div role="radiogroup" aria-label="发送键">
            <p className="px-2.5 pb-0.5 text-2xs text-muted-foreground">
              发送键
            </p>
            <OptionRow
              selected={sendMode === "enter"}
              label="Enter 发送"
              hint="Enter 发送消息，Shift+Enter 换行（输入法组词回车不触发）"
              onClick={() => setSendKeyMode("enter")}
            />
            <OptionRow
              selected={sendMode === "ctrl-enter"}
              label="Ctrl+Enter 发送"
              hint="Ctrl/Cmd+Enter 发送消息，Enter 换行"
              onClick={() => setSendKeyMode("ctrl-enter")}
            />
          </div>
          <div role="radiogroup" aria-label="审批卡快捷键">
            <p className="px-2.5 pb-0.5 text-2xs text-muted-foreground">
              审批卡快捷键
            </p>
            <OptionRow
              selected={escMode === "on"}
              label="Esc 拒绝 / 跳过"
              hint="审批卡 Esc=拒绝，提问卡 Esc=跳过作答（输入中有内容时不触发）"
              onClick={() => setEscActionMode("on")}
            />
            <OptionRow
              selected={escMode === "off"}
              label="关闭 Esc 快捷键"
              hint="Esc 不触发卡片动作"
              onClick={() => setEscActionMode("off")}
            />
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
