import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'
import { CheckCircle2, AlertTriangle, Info } from 'lucide-react'
import {
  Toast,
  ToastClose,
  ToastDescription,
  ToastProvider,
  ToastTitle,
  ToastViewport,
} from './toast'

type ToastVariant = 'default' | 'success' | 'destructive'

interface ToastItem {
  id: number
  title?: string
  description?: string
  variant: ToastVariant
}

interface ToastApi {
  toast: (opts: { title?: string; description?: string; variant?: ToastVariant }) => void
  success: (title: string, description?: string) => void
  error: (title: string, description?: string) => void
}

const ToastContext = createContext<ToastApi | null>(null)

let counter = 0

const icons: Record<ToastVariant, ReactNode> = {
  default: (
    <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-primary/15 bg-primary/10 text-primary">
      <Info className="h-4 w-4" aria-hidden />
    </span>
  ),
  success: (
    <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-success/20 bg-success/10 text-success">
      <CheckCircle2 className="h-4 w-4" aria-hidden />
    </span>
  ),
  destructive: (
    <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-destructive/20 bg-destructive/10 text-destructive">
      <AlertTriangle className="h-4 w-4" aria-hidden />
    </span>
  ),
}

export function ToastHost({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([])

  const remove = useCallback((id: number) => {
    setItems((prev) => prev.filter((item) => item.id !== id))
  }, [])

  const api = useMemo<ToastApi>(() => {
    const push = (opts: { title?: string; description?: string; variant?: ToastVariant }) => {
      const id = ++counter
      setItems((prev) => [...prev, { id, variant: 'default', ...opts }])
    }
    return {
      toast: push,
      success: (title, description) => push({ title, description, variant: 'success' }),
      error: (title, description) => push({ title, description, variant: 'destructive' }),
    }
  }, [])

  return (
    <ToastContext.Provider value={api}>
      <ToastProvider swipeDirection="right" duration={4200}>
        {children}
        {items.map((item) => (
          <Toast
            key={item.id}
            variant={item.variant}
            onOpenChange={(open) => {
              if (!open) remove(item.id)
            }}
          >
            {icons[item.variant]}
            <div className="min-w-0 flex-1">
              {item.title && <ToastTitle>{item.title}</ToastTitle>}
              {item.description && <ToastDescription>{item.description}</ToastDescription>}
            </div>
            <ToastClose />
          </Toast>
        ))}
        <ToastViewport />
      </ToastProvider>
    </ToastContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components -- toast hook 与 Host 组件同文件
export function useToast() {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used within ToastHost')
  return ctx
}
