/*
Copyright (C) 2026 LIghtJUNction
*/
import { Component, type ErrorInfo, type ReactNode } from 'react'
import { withTranslation, type WithTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { copyToClipboard } from '@/lib/copy-to-clipboard'

interface Props extends WithTranslation {
  children: ReactNode
}

interface State {
  hasError: boolean
  error: Error | null
  errorInfo: ErrorInfo | null
}

class DrawingErrorBoundaryBase extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false, error: null, errorInfo: null }
  }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { hasError: true, error }
  }

  componentDidCatch(_error: Error, errorInfo: ErrorInfo) {
    this.setState({ errorInfo })
  }

  handleRecover = () => {
    this.setState({ hasError: false, error: null, errorInfo: null })
  }

  handleCopyDiagnostics = async () => {
    const { t } = this.props
    const details = {
      timestamp: new Date().toISOString(),
      error: this.state.error?.message,
      stack: this.state.error?.stack,
      componentStack: this.state.errorInfo?.componentStack,
    }
    const success = await copyToClipboard(JSON.stringify(details, null, 2))
    if (success) {
      toast.success(t('Error details copied'))
    }
  }

  render() {
    const { hasError, error } = this.state
    const { children, t } = this.props

    if (hasError) {
      return (
        <div className='mx-auto my-8 max-w-2xl p-4'>
          <Alert variant='destructive'>
            <AlertTitle>{t('Oops! Something went wrong')}</AlertTitle>
            <AlertDescription className='space-y-2'>
              <p>
                {t(
                  'The drawing workbench encountered an error, but your draft has been saved.'
                )}
              </p>
              {error?.message ? (
                <p className='text-destructive-foreground/80 rounded bg-black/20 p-2 font-mono text-xs'>
                  {error.message}
                </p>
              ) : null}
            </AlertDescription>
            <AlertAction className='gap-2'>
              <Button
                type='button'
                size='sm'
                variant='outline'
                onClick={this.handleRecover}
              >
                {t('Recover workbench')}
              </Button>
              <Button
                type='button'
                size='sm'
                variant='secondary'
                onClick={() => void this.handleCopyDiagnostics()}
              >
                {t('Copy error details')}
              </Button>
            </AlertAction>
          </Alert>
        </div>
      )
    }

    return children
  }
}

export const DrawingErrorBoundary = withTranslation()(DrawingErrorBoundaryBase)
