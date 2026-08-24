import { forwardRef } from 'react'
import { cn } from '@/lib/utils'

/*
 * 34px 高、卡片底、玫红 focus（border + 3px wash ring）。
 */
export type InputProps = React.InputHTMLAttributes<HTMLInputElement>

export const Input = forwardRef<HTMLInputElement, InputProps>(({ className, type, ...props }, ref) => (
  <input
    type={type}
    ref={ref}
    className={cn(
      'h-[34px] w-full min-w-0 rounded-md border border-input bg-card px-3 text-sm text-foreground transition-[border-color,box-shadow] duration-200',
      'placeholder:text-muted-foreground',
      'focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash',
      'disabled:cursor-not-allowed disabled:opacity-50',
      'file:border-0 file:bg-transparent file:text-sm file:font-medium',
      className,
    )}
    {...props}
  />
))
Input.displayName = 'Input'

export type TextareaProps = React.TextareaHTMLAttributes<HTMLTextAreaElement>

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      'flex min-h-[80px] w-full rounded-md border border-input bg-card px-3 py-2 text-sm text-foreground transition-[border-color,box-shadow] duration-200',
      'placeholder:text-muted-foreground focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash',
      'disabled:cursor-not-allowed disabled:opacity-50',
      className,
    )}
    {...props}
  />
))
Textarea.displayName = 'Textarea'
