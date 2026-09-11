import { useCallback, useEffect, useRef, useState } from 'react'
import { createConversation, listConversations, listMessages } from './chat-api'
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
  const [error, setError] = useState<string>()
  const messageRequestSequence = useRef(0)

  const activeConversation = conversations.find((conversation) => conversation.id === activeConversationID)

  const refreshConversations = useCallback(async () => {
    setIsLoadingConversations(true)
    try {
      const next = await listConversations()
      setConversations(next)
      setActiveConversationID((current) => current && next.some((item) => item.id === current) ? current : next[0]?.id)
      setError(undefined)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法读取会话列表。')
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

  useEffect(() => { void refreshConversations() }, [refreshConversations])
  useEffect(() => { void refreshMessages() }, [refreshMessages])

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

  return (
    <div className="chat-workspace">
      <ConversationSidebar
        conversations={conversations}
        activeConversationID={activeConversationID}
        isLoading={isLoadingConversations}
        onCreate={() => void handleCreate()}
        onSelect={handleSelect}
      />
      {activeConversation && loadedConversationID === activeConversation.id ? (
        <ChatPane
          key={`${activeConversation.id}-${historyVersion}`}
          conversation={activeConversation}
          initialMessages={persistedMessages.map(toUIMessage)}
          isLoadingHistory={isLoadingMessages}
          onFinished={() => { void refreshConversations(); void refreshMessages() }}
        />
      ) : activeConversation ? (
        <main className="chat-pane no-conversation"><div className="server-state"><p>正在读取会话…</p></div></main>
      ) : (
        <main className="chat-pane no-conversation">
          <div className="server-state">
            <h1>{error ? '暂时无法连接服务' : '创建第一段对话'}</h1>
            <p>{error ?? '从一个问题开始，让学习过程留下可回溯的记录。'}</p>
            {!error && <button type="button" className="start-chat-button" onClick={() => void handleCreate()}>新建对话</button>}
          </div>
        </main>
      )}
    </div>
  )
}
