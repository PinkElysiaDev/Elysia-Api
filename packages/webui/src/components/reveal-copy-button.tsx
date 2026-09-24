import { useState } from "react";
import { Check, Copy, Eye, EyeOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/ui/use-toast";
import { api } from "@/lib/api";
import { copyText } from "@/lib/clipboard";

/** 复制成功的对勾复位延时。 */
const COPY_RESET_MS = 1500;

/**
 * RevealCopyButton：令牌凭证的显示/隐藏 + 复制（明文经 reveal 端点按需
 * 取回，可无限次查看）。API Key 页与运行配置页共用。
 */
export function RevealCopyButton({
  name,
  maskedToken,
}: {
  name: string;
  maskedToken: string;
}) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const [revealed, setRevealed] = useState(false);
  const [revealedToken, setRevealedToken] = useState("");
  const [busy, setBusy] = useState(false);

  async function handleReveal() {
    if (revealed) {
      setRevealed(false);
      setRevealedToken("");
      return;
    }
    setBusy(true);
    try {
      const { token } = await api.revealToken(name);
      setRevealedToken(token);
      setRevealed(true);
    } catch (err) {
      toast.error("获取失败", (err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function handleCopy() {
    setBusy(true);
    try {
      const token = revealed ? revealedToken : (await api.revealToken(name)).token;
      await copyText(token);
      setCopied(true);
      setTimeout(() => setCopied(false), COPY_RESET_MS);
    } catch (err) {
      toast.error("复制失败", (err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <span className="font-mono text-xs">
        {revealed ? revealedToken : maskedToken}
      </span>
      <Button
        variant="ghost"
        size="iconSm"
        title={revealed ? "隐藏" : "显示完整 Key"}
        aria-label={revealed ? "隐藏完整 Key" : "显示完整 Key"}
        disabled={busy}
        onClick={handleReveal}
      >
        {revealed ? (
          <EyeOff className="h-3.5 w-3.5" />
        ) : (
          <Eye className="h-3.5 w-3.5" />
        )}
      </Button>
      <Button
        variant="ghost"
        size="iconSm"
        title="复制完整 Key"
        aria-label="复制完整 Key"
        disabled={busy}
        onClick={handleCopy}
      >
        {copied ? (
          <Check className="h-3.5 w-3.5 text-jade" />
        ) : (
          <Copy className="h-3.5 w-3.5" />
        )}
      </Button>
    </>
  );
}
