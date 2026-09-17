type ToolActivityProps = {
  toolName?: string
  title?: string
  input?: unknown
  output?: unknown
  errorText?: string
  state?: string
}

function json(value: unknown) {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function presentationText(value: unknown) {
  if (value && typeof value === 'object' && 'summary' in value && typeof value.summary === 'string') {
    return value.summary
  }
  return json(value)
}

export function ToolActivity({ toolName, title: presentationTitle, input, output, errorText, state }: ToolActivityProps) {
  const title = presentationTitle || toolName || '工具调用'
  const status = errorText ? '调用失败' : state === 'input-streaming' || state === 'input-available' ? '调用中…' : '已完成'
  const detail = errorText || presentationText(output ?? input)
  return (
    <section className="tool-activity" aria-label={`${title} ${status}`}>
      <div className="tool-activity-title">
        <span>{title}</span>
        <span className={`tool-activity-badge ${errorText ? 'failed' : ''}`}>{status}</span>
      </div>
      <p className={errorText ? 'tool-error' : undefined}>{detail}</p>
    </section>
  )
}
