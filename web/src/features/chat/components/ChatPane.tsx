import { useChat } from '@ai-sdk/react'
import { DefaultChatTransport } from 'ai'
import type { UIMessage } from 'ai'
import { useRef, useState } from 'react'
import type { FormEvent, KeyboardEvent } from 'react'
import { cancelGeneration } from '../chat-api'
import type { Conversation } from '../types'
import { MarkdownContent } from './MarkdownContent'
import { ToolActivity } from './ToolActivity'

type Props = {
  conversation: Conversation
  initialMessages: UIMessage[]
  isLoadingHistory: boolean
  onFinished: () => void
}

type ToolPart = {
  type: string
  toolName?: string
  input?: unknown
  output?: unknown
  errorText?: string
  state?: string
}

type AgentStatusPart = {
  type: string
  data?: { phase?: string; stepId?: string }
}

function messageText(message: { parts: Array<{ type: string; text?: string }> }) {
  return message.parts
    .filter((part) => part.type === 'text')
    .map((part) => part.text ?? '')
    .join('')
}

export function ChatPane({ conversation, initialMessages, isLoadingHistory, onFinished }: Props) {
  const [input, setInput] = useState('')
  const assistantMessageID = useRef<string | null>(null)

  const [transport] = useState(() => new DefaultChatTransport({
    api: '/api/v1/chat',
    prepareSendMessagesRequest: ({ id, messages }) => ({
      body: { id, message: messages.at(-1) },
    }),
    fetch: async (input, init) => {
      const response = await fetch(input, init)
      assistantMessageID.current = response.headers.get('X-Assistant-Message-ID')
      return response
    },
  }))

  const { messages, sendMessage, status, stop, error } = useChat({
    id: conversation.id,
    messages: initialMessages,
    transport,
    throttle: 30,
    onFinish: onFinished,
  })
  const isGenerating = status === 'submitted' || status === 'streaming'

  async function submitText() {
    const text = input.trim()
    if (!text || isGenerating) return
    setInput('')
    await sendMessage({ text })
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void submitText()
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      void submitText()
    }
  }

  async function handleStop() {
    const latestAssistant = [...messages].reverse().find((message) => message.role === 'assistant')
    const id = assistantMessageID.current ?? latestAssistant?.id
    stop()
    if (id) await cancelGeneration(id).catch(() => undefined)
    onFinished()
  }

  return (
    <main className="chat-pane">
      <header className="chat-header">
        <span className="header-folder">□</span>
        <span className="chat-title">{conversation.title || '新对话'}</span>
        <span className="header-more">•••</span>
      </header>

      <section className="message-scroll" aria-live="polite">
        <div className="message-list">
          {isLoadingHistory && <p className="loading-messages">正在读取会话…</p>}
          {!isLoadingHistory && messages.length === 0 && (
            <div className="chat-empty-state">
              <span className="empty-logo">M</span>
              <h1>今天想一起解决什么？</h1>
              <p>可以让我帮你梳理思路、解释概念，或开始一次学习复盘。</p>
            </div>
          )}
          {messages.map((message) => {
            const content = messageText(message)
            if (!content && message.role !== 'assistant') return null
            const isLatestAssistant = message.role === 'assistant' && message.id === messages.at(-1)?.id
            const isWaitingForText = isLatestAssistant && isGenerating
            return (
              <article className={`chat-message ${message.role}`} key={message.id}>
                {message.role === 'assistant' ? (
                  <div className="assistant-content">
                    {message.parts.map((part, index) => {
                      if (part.type === 'text') {
                        return part.text ? <MarkdownContent key={`${message.id}-text-${index}`} content={part.text} /> : null
                      }
                      if (part.type === 'dynamic-tool') {
                        const tool = part as ToolPart
                        return <ToolActivity
                          key={`${message.id}-tool-${index}`}
                          toolName={tool.toolName}
                          input={tool.input}
                          output={tool.output}
                          errorText={tool.errorText}
                          state={tool.state}
                        />
                      }
                      if (part.type === 'data-agent-status') {
                        const statusPart = part as AgentStatusPart
                        const phase = statusPart.data?.phase
                        if (!phase || phase === 'step-start' || phase === 'step-finish' || phase === 'answering') return null
                        return <p className="agent-status" key={`${message.id}-status-${index}`}>
                          {phase === 'calling-tool' ? '正在调用工具…' : '正在思考…'}
                        </p>
                      }
                      return null
                    })}
                    {!content && isWaitingForText ? (
                      <span className="typing-cursor" aria-label="正在思考" />
                    ) : !content && !isWaitingForText ? (
                      <p className="assistant-failure">生成失败，请重新生成。</p>
                    ) : null}
                  </div>
                ) : (
                  <div className="user-bubble">{content}</div>
                )}
              </article>
            )
          })}
          {error && <p className="chat-error">{error.message || '发送失败，请重试。'}</p>}
        </div>
      </section>

      <div className="composer-area">
        <form className="composer" onSubmit={handleSubmit}>
          <textarea
            aria-label="输入消息"
            placeholder="给 Memory Agent 发消息"
            rows={1}
            value={input}
            onChange={(event) => setInput(event.target.value)}
            onKeyDown={handleKeyDown}
          />
          <div className="composer-actions">
            <button className="composer-plus" type="button" aria-label="添加内容">＋</button>
            <span className="composer-hint">{isGenerating ? '正在生成回答…' : 'Enter 发送，Shift + Enter 换行'}</span>
            {isGenerating ? (
              <button className="stop-button" type="button" onClick={() => void handleStop()}>停止</button>
            ) : (
              <button className="send-button" type="submit" disabled={!input.trim()} aria-label="发送消息">↑</button>
            )}
          </div>
        </form>
      </div>
    </main>
  )
}
