import * as ToastPrimitives from '@radix-ui/react-toast'
import { cva, type VariantProps } from 'class-variance-authority'
import { X } from 'lucide-react'
import { forwardRef } from 'react'
import { cn } from '@/lib/utils'

export const ToastProvider = ToastPrimitives.Provider

/* 底部居中，宽度稳定以避免长短文案导致卡片跳变。 */
export const ToastViewport = forwardRef<
  React.ElementRef<typeof ToastPrimitives.Viewport>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitives.Viewport>
>(({ className, ...props }, ref) => (
  <ToastPrimitives.Viewport
    ref={ref}
    className={cn(
      'pointer-events-none fixed bottom-6 left-1/2 z-[90] flex w-[min(420px,calc(100%-32px))] -translate-x-1/2 flex-col items-stretch gap-2.5 outline-none',
      className,
    )}
    {...props}
  />
))
ToastViewport.displayName = ToastPrimitives.Viewport.displayName

/* 轻量通知卡：无装饰顶线，状态由图标与整体边框表达。 */
const toastVariants = cva(
  'group pointer-events-auto relative flex w-full items-start gap-3 overflow-hidden rounded-xl border bg-card/95 px-3.5 py-3 pr-10 text-card-foreground shadow-lg backdrop-blur-md '
    + 'data-[state=open]:animate-toast-in data-[state=closed]:animate-toast-out motion-reduce:animate-none '
    + 'data-[swipe=move]:translate-x-[var(--radix-toast-swipe-move-x)] data-[swipe=move]:transition-none '
    + 'data-[swipe=cancel]:translate-x-0 data-[swipe=cancel]:transition-transform data-[swipe=cancel]:duration-200 '
    + 'data-[swipe=end]:translate-x-[var(--radix-toast-swipe-end-x)]',
  {
    variants: {
      variant: {
        default: 'border-border/90',
        success: 'border-success/25',
        destructive: 'border-destructive/30',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export const Toast = forwardRef<
  React.ElementRef<typeof ToastPrimitives.Root>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitives.Root> & VariantProps<typeof toastVariants>
>(({ className, variant, ...props }, ref) => (
  <ToastPrimitives.Root ref={ref} className={cn(toastVariants({ variant }), className)} {...props} />
))
Toast.displayName = ToastPrimitives.Root.displayName

export const ToastClose = forwardRef<
  React.ElementRef<typeof ToastPrimitives.Close>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitives.Close>
>(({ className, ...props }, ref) => (
  <ToastPrimitives.Close
    ref={ref}
    className={cn(
      'absolute right-2 top-2 rounded-md p-1 text-muted-foreground/70 transition-colors hover:bg-wash hover:text-foreground focus-visible:text-foreground',
      className,
    )}
    toast-close=""
    aria-label="关闭通知"
    {...props}
  >
    <X className="h-3.5 w-3.5" aria-hidden />
  </ToastPrimitives.Close>
))
ToastClose.displayName = ToastPrimitives.Close.displayName

export const ToastTitle = forwardRef<
  React.ElementRef<typeof ToastPrimitives.Title>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitives.Title>
>(({ className, ...props }, ref) => (
  <ToastPrimitives.Title ref={ref} className={cn('text-sm font-semibold leading-5', className)} {...props} />
))
ToastTitle.displayName = ToastPrimitives.Title.displayName

export const ToastDescription = forwardRef<
  React.ElementRef<typeof ToastPrimitives.Description>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitives.Description>
>(({ className, ...props }, ref) => (
  <ToastPrimitives.Description
    ref={ref}
    className={cn('break-words text-xs leading-5 text-muted-foreground', className)}
    {...props}
  />
))
ToastDescription.displayName = ToastPrimitives.Description.displayName
