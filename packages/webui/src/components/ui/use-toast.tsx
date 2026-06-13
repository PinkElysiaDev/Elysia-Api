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
  default: <Info className="mt-0.5 h-5 w-5 text-primary" />,
  success: <CheckCircle2 className="mt-0.5 h-5 w-5 text-success" />,
  destructive: <AlertTriangle className="mt-0.5 h-5 w-5 text-destructive" />,
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
            <div className="grid gap-0.5">
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

export function useToast() {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used within ToastHost')
  return ctx
}
