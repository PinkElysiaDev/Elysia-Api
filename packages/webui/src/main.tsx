import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { ErrorBoundary } from './components/error-boundary'
import { ThemeProvider } from './lib/theme'
import { ToastHost } from './components/ui/use-toast'
import { TooltipProvider } from './components/ui/tooltip'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ErrorBoundary>
      <ThemeProvider>
        <ToastHost>
          <TooltipProvider delayDuration={200}>
            <App />
          </TooltipProvider>
        </ToastHost>
      </ThemeProvider>
    </ErrorBoundary>
  </StrictMode>,
)
