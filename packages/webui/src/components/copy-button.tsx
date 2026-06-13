import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { Button } from './ui/button'
import type { ButtonProps } from './ui/button'

export function CopyButton({ value, ...props }: { value: string } & Omit<ButtonProps, 'onClick'>) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant="ghost"
      size="iconSm"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value)
          setCopied(true)
          setTimeout(() => setCopied(false), 1500)
        } catch {
          /* clipboard unavailable */
        }
      }}
      aria-label="复制"
      {...props}
    >
      {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
    </Button>
  )
}
