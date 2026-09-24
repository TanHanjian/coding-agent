import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertTriangle, Loader2, MessageSquarePlus, RefreshCw, ServerOff, Sparkles, Terminal } from 'lucide-react'
import { createConversation, deleteConversation, listConversations, listMessages } from './chat-api'
import { ChatPane } from './components/ChatPane'
import { ConversationSidebar } from './components/ConversationSidebar'
import './chat.css'
import type { Conversation, PersistedMessage } from './types'
import { toUIMessage } from './types'

export function ChatWorkspace() {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [activeConversationID, setActiveConversationID] = useState<string>()
  const [persistedMessages, setPersistedMessages] = useState<PersistedMessage[]>([])
  const [loadedConversationID, setLoadedConversationID] = useState<string>()
  const [historyVersion, setHistoryVersion] = useState(0)
  const [isLoadingConversations, setIsLoadingConversations] = useState(true)
  const [isLoadingMessages, setIsLoadingMessages] = useState(false)
  const [deletingConversationID, setDeletingConversationID] = useState<string>()
  const [isRetrying, setIsRetrying] = useState(false)
  const [error, setError] = useState<string>()
  const messageRequestSequence = useRef(0)

  const activeConversation = conversations.find((conversation) => conversation.id === activeConversationID)

  const refreshConversations = useCallback(async () => {
    setIsLoadingConversations(true)
    try {
      const next = await listConversations()
      setConversations(next)
      setActiveConversationID((current) => (current && next.some((item) => item.id === current) ? current : next[0]?.id))
      setError(undefined)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法连接后端服务。')
    } finally {
      setIsLoadingConversations(false)
    }
  }, [])

  const refreshMessages = useCallback(async () => {
    const requestSequence = ++messageRequestSequence.current
    if (!activeConversationID) {
      setPersistedMessages([])
      setLoadedConversationID(undefined)
      return
    }
    setIsLoadingMessages(true)
    try {
      const messages = await listMessages(activeConversationID)
      if (requestSequence !== messageRequestSequence.current) return
      setPersistedMessages(messages)
      setLoadedConversationID(activeConversationID)
      setHistoryVersion((current) => current + 1)
      setError(undefined)
    } catch (reason) {
      if (requestSequence !== messageRequestSequence.current) return
      setError(reason instanceof Error ? reason.message : '无法读取消息。')
    } finally {
      if (requestSequence === messageRequestSequence.current) setIsLoadingMessages(false)
    }
  }, [activeConversationID])

  useEffect(() => {
    void refreshConversations()
  }, [refreshConversations])

  useEffect(() => {
    void refreshMessages()
  }, [refreshMessages])

  async function handleRetry() {
    setIsRetrying(true)
    await refreshConversations()
    if (activeConversationID) {
      await refreshMessages()
    }
    setIsRetrying(false)
  }

  async function handleCreate() {
    try {
      const created = await createConversation()
      setConversations((current) => [created, ...current])
      setActiveConversationID(created.id)
      setPersistedMessages([])
      setLoadedConversationID(created.id)
      setHistoryVersion((current) => current + 1)
      setError(undefined)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法创建会话。')
    }
  }

  function handleSelect(conversationID: string) {
    setLoadedConversationID(undefined)
    setPersistedMessages([])
    setActiveConversationID(conversationID)
  }

  async function handleDelete(conversation: Conversation) {
    if (!window.confirm(`确定删除“${conversation.title.trim() || '未命名会话'}”吗？其中的全部消息也会被删除。`)) return
    setDeletingConversationID(conversation.id)
    try {
      await deleteConversation(conversation.id)
      const next = conversations.filter((item) => item.id !== conversation.id)
      setConversations(next)
      if (activeConversationID === conversation.id) {
        messageRequestSequence.current += 1
        setPersistedMessages([])
        setLoadedConversationID(undefined)
        setActiveConversationID(next[0]?.id)
      }
      setError(undefined)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法删除会话。')
    } finally {
      setDeletingConversationID(undefined)
    }
  }

  return (
    <div className="chat-workspace">
      <ConversationSidebar
        conversations={conversations}
        activeConversationID={activeConversationID}
        isLoading={isLoadingConversations}
        deletingConversationID={deletingConversationID}
        onCreate={() => void handleCreate()}
        onSelect={handleSelect}
        onDelete={(conversation) => void handleDelete(conversation)}
      />

      {activeConversation && loadedConversationID === activeConversation.id ? (
        <ChatPane
          key={`${activeConversation.id}-${historyVersion}`}
          conversation={activeConversation}
          initialMessages={persistedMessages.map(toUIMessage)}
          isLoadingHistory={isLoadingMessages}
          onFinished={() => {
            void refreshConversations()
            void refreshMessages()
          }}
        />
      ) : activeConversation ? (
        <main className="chat-pane no-conversation">
          <div className="state-card-container">
            <Loader2 size={32} className="spin-icon primary" />
            <h3 className="state-card-title">正在载入会话…</h3>
            <p className="state-card-desc">正在同步历史消息与上下文</p>
          </div>
        </main>
      ) : error ? (
        <main className="chat-pane no-conversation">
          <div className="state-card-container error-state">
            <div className="state-icon-badge error">
              <ServerOff size={28} />
            </div>
            <h2 className="state-card-title">暂时无法连接后端服务</h2>
            <div className="error-reason-pill">
              <AlertTriangle size={14} />
              <span>{error}</span>
            </div>
            <p className="state-card-desc">
              本地后端可能尚未启动或代理未就绪。请确认后端服务已在 <code>127.0.0.1:8080</code> 正常监听。
            </p>

            <div className="state-troubleshoot-box">
              <div className="troubleshoot-header">
                <Terminal size={14} />
                <span>后端启动参考</span>
              </div>
              <code>go run ./cmd/server 或 make dev</code>
            </div>

            <button
              type="button"
              className="retry-connection-button"
              disabled={isRetrying || isLoadingConversations}
              onClick={() => void handleRetry()}
            >
              <RefreshCw size={15} className={isRetrying || isLoadingConversations ? 'spin-icon' : ''} />
              <span>{isRetrying || isLoadingConversations ? '正在重新连接…' : '重新连接服务'}</span>
            </button>
          </div>
        </main>
      ) : (
        <main className="chat-pane no-conversation">
          <div className="state-card-container welcome-fallback">
            <div className="state-icon-badge brand">
              <Sparkles size={28} />
            </div>
            <h2 className="state-card-title">开启第一段学习对话</h2>
            <p className="state-card-desc">从一个问题开始，让每一次思考与学习过程留下可回溯的沉淀。</p>
            <button type="button" className="start-chat-button" onClick={() => void handleCreate()}>
              <MessageSquarePlus size={16} />
              <span>新建会话</span>
            </button>
          </div>
        </main>
      )}
    </div>
  )
}
