import { forwardRef, useState } from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { Input, type InputProps } from './ui/input'
import { cn } from '@/lib/utils'

/** Secret 输入框：默认隐藏，可切换可见。用于 apiKey / token 明文录入。 */
export const SecretInput = forwardRef<HTMLInputElement, InputProps>(({ className, ...props }, ref) => {
  const [visible, setVisible] = useState(false)
  return (
    <div className="relative">
      <Input ref={ref} type={visible ? 'text' : 'password'} className={cn('pr-10', className)} {...props} />
      <button
        type="button"
        disabled={props.disabled}
        onClick={() => setVisible((v) => !v)}
        className="absolute inset-y-0 right-0 flex w-10 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-wash hover:text-rose disabled:pointer-events-none disabled:opacity-50 max-rail:w-11"
        aria-label={visible ? '隐藏' : '显示'}
        aria-pressed={visible}
      >
        {visible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
      </button>
    </div>
  )
})
SecretInput.displayName = 'SecretInput'
