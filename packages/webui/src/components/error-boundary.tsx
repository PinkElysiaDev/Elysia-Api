import { Component, type ErrorInfo, type ReactNode } from 'react'
import { AlertTriangle } from 'lucide-react'

interface ErrorBoundaryProps {
  children: ReactNode
}

interface ErrorBoundaryState {
  error: Error | null
}

/**
 * 根部错误边界：任何未捕获的渲染异常都不应让整个管理面板变成空白页，
 * 而是展示可重试的降级界面。
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('[webui] unhandled render error', error, info.componentStack)
  }

  private handleReload = () => {
    this.setState({ error: null })
  }

  render() {
    if (!this.state.error) {
      return this.props.children
    }
    return (
      <div className="flex min-h-[60vh] flex-col items-center justify-center gap-3 px-6 text-center">
        <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-destructive/12 text-destructive">
          <AlertTriangle className="h-7 w-7" />
        </span>
        <p className="text-base font-semibold">页面渲染失败</p>
        <p className="max-w-md text-sm text-muted-foreground">
          {this.state.error.message || '发生了未知错误，请重试或刷新页面。'}
        </p>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={this.handleReload}
            className="rounded-lg border border-border bg-background px-4 py-2 text-sm font-medium hover:bg-accent"
          >
            重试
          </button>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="rounded-lg border border-border bg-background px-4 py-2 text-sm font-medium hover:bg-accent"
          >
            刷新页面
          </button>
        </div>
      </div>
    )
  }
}
