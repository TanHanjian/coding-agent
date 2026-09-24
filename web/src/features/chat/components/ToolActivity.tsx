import { useState } from 'react'
import { AlertCircle, CheckCircle2, ChevronDown, ChevronRight, Loader2 } from 'lucide-react'

type ToolActivityProps = {
  toolName?: string
  title?: string
  input?: unknown
  output?: unknown
  errorText?: string
  state?: string
}

function formatJson(value: unknown) {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function getSummary(value: unknown) {
  if (value && typeof value === 'object' && 'summary' in value && typeof value.summary === 'string') {
    return value.summary
  }
  return null
}

export function ToolActivity({ toolName, title: presentationTitle, input, output, errorText, state }: ToolActivityProps) {
  const [isOpen, setIsOpen] = useState(false)
  
  const title = presentationTitle || toolName || '工具执行'
  const isRunning = state === 'input-streaming' || state === 'input-available'
  const isFailed = Boolean(errorText)

  const summary = isFailed ? errorText : getSummary(output ?? input)
  const hasDetails = Boolean(input || output || errorText)

  return (
    <div className={`tool-activity-card ${isFailed ? 'failed' : isRunning ? 'running' : 'completed'}`}>
      <button
        type="button"
        className="tool-activity-header"
        onClick={() => hasDetails && setIsOpen(!isOpen)}
        disabled={!hasDetails}
        aria-expanded={isOpen}
      >
        <div className="tool-header-left">
          <span className="tool-icon-wrapper">
            {isRunning ? (
              <Loader2 size={14} className="tool-spinner" />
            ) : isFailed ? (
              <AlertCircle size={14} className="tool-icon-failed" />
            ) : (
              <CheckCircle2 size={14} className="tool-icon-success" />
            )}
          </span>
          <span className="tool-title-text">{title}</span>
        </div>

        <div className="tool-header-right">
          <span className="tool-badge">
            {isRunning ? '执行中' : isFailed ? '失败' : '完成'}
          </span>
          {hasDetails && (
            <span className="tool-expand-icon">
              {isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            </span>
          )}
        </div>
      </button>

      {summary && !isOpen && (
        <div className={`tool-summary-line ${isFailed ? 'error' : ''}`}>
          {summary}
        </div>
      )}

      {isOpen && (
        <div className="tool-details-body">
          {input !== undefined && (
            <div className="tool-payload-section">
              <span className="payload-label">输入参数 (Input)</span>
              <pre className="payload-content"><code>{formatJson(input)}</code></pre>
            </div>
          )}
          {output !== undefined && (
            <div className="tool-payload-section">
              <span className="payload-label">输出结果 (Output)</span>
              <pre className="payload-content"><code>{formatJson(output)}</code></pre>
            </div>
          )}
          {errorText && (
            <div className="tool-payload-section error">
              <span className="payload-label">错误信息 (Error)</span>
              <pre className="payload-content error"><code>{errorText}</code></pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
