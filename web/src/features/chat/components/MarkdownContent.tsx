import { useState } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Check, Copy } from 'lucide-react'

type CodeProps = React.ComponentProps<'code'>

function CodeBlock({ children, className, ...props }: CodeProps) {
  const match = /language-(\w+)/.exec(className || '')
  const rawCode = String(children).replace(/\n$/, '')
  const isInline = !match && !rawCode.includes('\n')
  const [copied, setCopied] = useState(false)

  if (isInline) {
    return (
      <code className="inline-code" {...props}>
        {children}
      </code>
    )
  }

  const language = match ? match[1] : 'code'

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(rawCode)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // ignore
    }
  }

  return (
    <div className="code-block-container">
      <div className="code-block-header">
        <span className="code-block-lang">{language}</span>
        <button
          type="button"
          className="code-copy-btn"
          onClick={() => void handleCopy()}
          aria-label="复制代码"
        >
          {copied ? (
            <>
              <Check size={13} className="copy-icon-success" />
              <span>已复制</span>
            </>
          ) : (
            <>
              <Copy size={13} />
              <span>复制</span>
            </>
          )}
        </button>
      </div>
      <pre className="code-block-pre">
        <code className={className} {...props}>
          {children}
        </code>
      </pre>
    </div>
  )
}

export function MarkdownContent({ content }: { content: string }) {
  return (
    <Markdown
      remarkPlugins={[remarkGfm]}
      components={{
        pre: ({ children }) => <>{children}</>,
        code: CodeBlock,
        table: ({ children }) => (
          <div className="markdown-table-wrapper">
            <table>{children}</table>
          </div>
        ),
        a: ({ href, children }) => (
          <a href={href} target="_blank" rel="noreferrer" className="markdown-link">
            {children}
          </a>
        ),
      }}
    >
      {content}
    </Markdown>
  )
}
