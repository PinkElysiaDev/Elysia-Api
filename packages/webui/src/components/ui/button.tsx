import { forwardRef } from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

/*
 * 管理面按钮变体：
 * 默认 = 卡片底 + 刻线边 + 6px 角的仪器按钮；primary = 品牌渐变；
 * ghost = 无边框图标钮；danger = ghost 的 ember hover。
 */
const buttonVariants = cva(
  'inline-flex items-center justify-center gap-[7px] whitespace-nowrap rounded-md text-sm font-medium transition-[color,border-color,background-color,box-shadow,transform,filter] duration-200 '
    + 'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background '
    + 'disabled:pointer-events-none disabled:opacity-45 active:translate-y-px [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        default:
          'border border-input bg-card text-foreground shadow-[0_1px_2px_rgba(0,0,0,0.04)] hover:border-rose hover:bg-wash hover:text-rose',
        primary:
          'border border-transparent bg-brand-grad text-white shadow-pri-glow hover:brightness-[1.06] hover:text-white',
        ghost:
          'border border-transparent text-muted-foreground hover:border-border hover:bg-wash hover:text-rose',
        danger:
          'border border-transparent text-muted-foreground hover:border-transparent hover:bg-[color-mix(in_srgb,var(--ember)_8%,transparent)] hover:text-ember',
        outline: 'border border-border bg-transparent hover:bg-wash hover:text-rose hover:border-rose',
        destructive: 'bg-destructive text-destructive-foreground shadow-sm hover:brightness-110',
      },
      size: {
        default: 'h-[34px] px-3.5 [&_svg]:size-4',
        sm: 'h-[29px] rounded-md px-2.5 text-xs [&_svg]:size-[15px]',
        lg: 'h-11 px-6 [&_svg]:size-4',
        icon: 'h-[34px] w-[34px] [&_svg]:size-4',
        iconSm: 'h-[29px] w-[29px] [&_svg]:size-[15px]',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return <Comp className={cn(buttonVariants({ variant, size, className }))} ref={ref} {...props} />
  },
)
Button.displayName = 'Button'
