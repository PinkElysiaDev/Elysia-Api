import * as ToastPrimitives from '@radix-ui/react-toast'
import { cva, type VariantProps } from 'class-variance-authority'
import { X } from 'lucide-react'
import { forwardRef } from 'react'
import { cn } from '@/lib/utils'

export const ToastProvider = ToastPrimitives.Provider

export const ToastViewport = forwardRef<
  React.ElementRef<typeof ToastPrimitives.Viewport>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitives.Viewport>
>(({ className, ...props }, ref) => (
  <ToastPrimitives.Viewport
    ref={ref}
    className={cn(
      'fixed bottom-0 right-0 z-[100] flex max-h-screen w-full flex-col-reverse gap-2 p-4 sm:bottom-4 sm:right-4 sm:top-auto sm:w-[400px]',
      className,
    )}
    {...props}
  />
))
ToastViewport.displayName = ToastPrimitives.Viewport.displayName

const toastVariants = cva(
  'group pointer-events-auto relative flex w-full items-start gap-3 overflow-hidden rounded-xl border p-4 pr-8 shadow-glow transition-all data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-80 data-[state=open]:slide-in-from-right-full data-[state=closed]:slide-out-to-right-full',
  {
    variants: {
      variant: {
        default: 'border-border bg-card text-card-foreground',
        success: 'border-success/30 bg-card text-card-foreground',
        destructive: 'border-destructive/40 bg-card text-card-foreground',
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
      'absolute right-2 top-2 rounded-md p-1 text-muted-foreground/70 opacity-0 transition-opacity hover:text-foreground group-hover:opacity-100',
      className,
    )}
    toast-close=""
    {...props}
  >
    <X className="h-4 w-4" />
  </ToastPrimitives.Close>
))
ToastClose.displayName = ToastPrimitives.Close.displayName

export const ToastTitle = forwardRef<
  React.ElementRef<typeof ToastPrimitives.Title>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitives.Title>
>(({ className, ...props }, ref) => (
  <ToastPrimitives.Title ref={ref} className={cn('text-sm font-semibold', className)} {...props} />
))
ToastTitle.displayName = ToastPrimitives.Title.displayName

export const ToastDescription = forwardRef<
  React.ElementRef<typeof ToastPrimitives.Description>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitives.Description>
>(({ className, ...props }, ref) => (
  <ToastPrimitives.Description ref={ref} className={cn('text-sm text-muted-foreground', className)} {...props} />
))
ToastDescription.displayName = ToastPrimitives.Description.displayName
