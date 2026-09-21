import { useState } from 'react'
import { Check, Copy } from 'lucide-react'

/** 复制成功指示的停留时长。 */
const COPY_RESET_MS = 1500
import { Button } from './ui/button'
import type { ButtonProps } from './ui/button'
import { useToast } from './ui/use-toast'
import { copyText } from '@/lib/clipboard'

export function CopyButton({ value, ...props }: { value: string } & Omit<ButtonProps, 'onClick'>) {
  const [copied, setCopied] = useState(false)
  const toast = useToast()
  return (
    <Button
      variant="ghost"
      size="iconSm"
      onClick={async () => {
        try {
          await copyText(value)
          setCopied(true)
          setTimeout(() => setCopied(false), COPY_RESET_MS)
        } catch {
          toast.error('复制失败', '当前环境剪贴板不可用，请手动选中复制')
        }
      }}
      aria-label="复制"
      {...props}
    >
      {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
    </Button>
  )
}
