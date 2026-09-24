import { useChat } from '@ai-sdk/react'
import { DefaultChatTransport } from 'ai'
import type { UIMessage } from 'ai'
import { useEffect, useRef, useState } from 'react'
import type { FormEvent, KeyboardEvent } from 'react'
import {
  ArrowUp,
  Brain,
  Code2,
  Lightbulb,
  Loader2,
  MessageSquare,
  Sparkles,
  Square,
  User,
  Wrench,
} from 'lucide-react'
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
  title?: string
  input?: unknown
  output?: unknown
  errorText?: string
  state?: string
}

type AgentStatusPart = {
  type: string
  data?: { phase?: string; stepId?: string }
}

const QUICK_PROMPTS = [
  {
    icon: Brain,
    title: '知识体系梳理',
    desc: '系统梳理核心概念与认知框架',
    prompt: '帮我系统梳理一下常见数据结构及其复杂度与应用场景。',
  },
  {
    icon: Code2,
    title: '核心算法解构',
    desc: '剖析经典模板与边界条件',
    prompt: '解释一下单调栈的经典应用场景，并附上典型解题代码模板。',
  },
  {
    icon: Lightbulb,
    title: '概念通俗比喻',
    desc: '用生动的现实比喻理解复杂理论',
    prompt: '用通俗生动的比喻讲解一下分布式系统中的 Raft 共识算法。',
  },
  {
    icon: Wrench,
    title: '工程排查思路',
    desc: '梳理高可用与性能调优链路',
    prompt: '如何系统化排查与定位后端高并发场景下的内存泄漏与 CPU 飙高问题？',
  },
]

function messageText(message: { parts: Array<{ type: string; text?: string }> }) {
  return message.parts
    .filter((part) => part.type === 'text')
    .map((part) => part.text ?? '')
    .join('')
}

function hasLaterPhase(parts: readonly { type: string; data?: unknown }[], index: number, phase: string) {
  return parts.slice(index + 1).some((part) => {
    if (part.type !== 'data-agent-status' || !part.data || typeof part.data !== 'object') return false
    return 'phase' in part.data && part.data.phase === phase
  })
}

function toolCallIsActive(parts: readonly ToolPart[], index: number) {
  const tool = parts.slice(index + 1).find((part) => part.type === 'dynamic-tool')
  return !tool || tool.state === 'input-streaming' || tool.state === 'input-available'
}

export function ChatPane({ conversation, initialMessages, isLoadingHistory, onFinished }: Props) {
  const [input, setInput] = useState('')
  const assistantMessageID = useRef<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)

  const [transport] = useState(
    () =>
      new DefaultChatTransport({
        api: '/api/v1/chat',
        prepareSendMessagesRequest: ({ id, messages }) => ({
          body: { id, message: messages.at(-1) },
        }),
        fetch: async (input, init) => {
          const response = await fetch(input, init)
          assistantMessageID.current = response.headers.get('X-Assistant-Message-ID')
          return response
        },
      }),
  )

  const { messages, sendMessage, status, stop, error } = useChat({
    id: conversation.id,
    messages: initialMessages,
    transport,
    throttle: 30,
    onFinish: onFinished,
  })

  const isGenerating = status === 'submitted' || status === 'streaming'

  // 输入框自适应高度调整
  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 180)}px`
  }, [input])

  // 消息更新时自动滚动至底部
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, isGenerating])

  async function submitText(overrideText?: string) {
    const text = (overrideText ?? input).trim()
    if (!text || isGenerating) return
    setInput('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
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
      {/* 顶部导航 */}
      <header className="chat-header">
        <div className="chat-header-title-box">
          <MessageSquare size={17} className="header-icon" />
          <h2 className="chat-title">{conversation.title || '新对话'}</h2>
          <span className="chat-status-pill">
            <span className="chat-status-dot" />
            就绪
          </span>
        </div>
      </header>

      {/* 消息滚动区 */}
      <section className="message-scroll" aria-live="polite">
        <div className="message-list">
          {isLoadingHistory && (
            <div className="loading-state-box">
              <Loader2 size={20} className="spin-icon" />
              <span>正在载入会话记录…</span>
            </div>
          )}

          {!isLoadingHistory && messages.length === 0 && (
            <div className="chat-welcome-container">
              <div className="welcome-avatar-glow">
                <Sparkles size={28} className="welcome-sparkles" />
              </div>
              <h1 className="welcome-title">今天想一起探讨什么？</h1>
              <p className="welcome-subtitle">
                我是你的个人 <strong>Memory Agent</strong>，支持思路梳理、知识复盘与代码推演。
              </p>

              <div className="quick-prompts-grid">
                {QUICK_PROMPTS.map((item, idx) => {
                  const Icon = item.icon
                  return (
                    <button
                      key={idx}
                      type="button"
                      className="quick-prompt-card"
                      onClick={() => void submitText(item.prompt)}
                    >
                      <div className="prompt-card-header">
                        <Icon size={16} className="prompt-card-icon" />
                        <span className="prompt-card-title">{item.title}</span>
                      </div>
                      <p className="prompt-card-desc">{item.desc}</p>
                    </button>
                  )
                })}
              </div>
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
                  <div className="assistant-message-row">
                    <div className="assistant-avatar">
                      <Sparkles size={16} />
                    </div>
                    <div className="assistant-content">
                      {message.parts.map((part, index) => {
                        if (part.type === 'text') {
                          return part.text ? (
                            <MarkdownContent key={`${message.id}-text-${index}`} content={part.text} />
                          ) : null
                        }
                        if (part.type === 'dynamic-tool') {
                          const tool = part as ToolPart
                          return (
                            <ToolActivity
                              key={`${message.id}-tool-${index}`}
                              toolName={tool.toolName}
                              title={tool.title}
                              input={tool.input}
                              output={tool.output}
                              errorText={tool.errorText}
                              state={tool.state}
                            />
                          )
                        }
                        if (part.type === 'data-agent-status') {
                          const statusPart = part as AgentStatusPart
                          const phase = statusPart.data?.phase
                          if (!phase || phase === 'step-start' || phase === 'step-finish') return null
                          if (phase === 'thinking' && hasLaterPhase(message.parts, index, 'calling-tool')) return null
                          if (phase === 'calling-tool') {
                            return toolCallIsActive(message.parts as ToolPart[], index) ? (
                              <div className="agent-status-pill" key={`${message.id}-status-${index}`}>
                                <Loader2 size={13} className="spin-icon" />
                                <span>正在调用工具…</span>
                              </div>
                            ) : null
                          }
                          return (
                            <div className="agent-status-pill" key={`${message.id}-status-${index}`}>
                              <span className="pulse-dot" />
                              <span>{phase === 'answering' ? '正在撰写回答…' : '正在分析与思考…'}</span>
                            </div>
                          )
                        }
                        return null
                      })}

                      {!content && isWaitingForText ? (
                        <div className="typing-indicator">
                          <span className="typing-dot" />
                          <span className="typing-dot" />
                          <span className="typing-dot" />
                        </div>
                      ) : !content && !isWaitingForText ? (
                        <p className="assistant-failure">生成中断或失败，请重新尝试发送。</p>
                      ) : null}
                    </div>
                  </div>
                ) : (
                  <div className="user-message-row">
                    <div className="user-bubble">{content}</div>
                    <div className="user-avatar">
                      <User size={16} />
                    </div>
                  </div>
                )}
              </article>
            )
          })}

          {error && (
            <div className="chat-error-banner">
              <span>{error.message || '网络或接口异常，请稍后重试。'}</span>
            </div>
          )}

          <div ref={messagesEndRef} />
        </div>
      </section>

      {/* 底部悬浮输入区域 */}
      <div className="composer-area">
        <form className="composer-island" onSubmit={handleSubmit}>
          <textarea
            ref={textareaRef}
            aria-label="输入消息"
            placeholder="与 Memory Agent 对话... (Enter 发送, Shift + Enter 换行)"
            rows={1}
            value={input}
            onChange={(event) => setInput(event.target.value)}
            onKeyDown={handleKeyDown}
          />
          <div className="composer-actions">
            <span className="composer-hint">
              {isGenerating ? '正在实时生成解答…' : '按 Enter 发送，Shift + Enter 换行'}
            </span>

            {isGenerating ? (
              <button
                className="stop-button"
                type="button"
                onClick={() => void handleStop()}
                title="停止生成"
              >
                <Square size={13} className="stop-icon" />
                <span>停止</span>
              </button>
            ) : (
              <button
                className={`send-button ${input.trim() ? 'active' : ''}`}
                type="submit"
                disabled={!input.trim()}
                aria-label="发送消息"
                title="发送 (Enter)"
              >
                <ArrowUp size={16} />
              </button>
            )}
          </div>
        </form>
      </div>
    </main>
  )
}
