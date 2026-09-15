type ToolActivityProps = {
  toolName?: string
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

export function ToolActivity({ toolName, input, output, errorText, state }: ToolActivityProps) {
  const title = toolName || '工具调用'
  const status = errorText ? '调用失败' : state === 'input-streaming' || state === 'input-available' ? '调用中…' : '已完成'
  return (
    <details className="tool-activity">
      <summary>{`${title} ${status}`}</summary>
      {input !== undefined && <pre><strong>输入</strong>{`\n${json(input)}`}</pre>}
      {output !== undefined && <pre><strong>输出</strong>{`\n${json(output)}`}</pre>}
      {errorText && <p className="tool-error">{errorText}</p>}
    </details>
  )
}
