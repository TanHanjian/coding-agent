import { Bot, Loader2, MessageSquare, MessageSquareDashed, Plus, Sparkles, Trash2 } from 'lucide-react'
import type { Conversation } from '../types'

type Props = {
  conversations: Conversation[]
  activeConversationID?: string
  isLoading: boolean
  deletingConversationID?: string
  onCreate: () => void
  onSelect: (conversationID: string) => void
  onDelete: (conversation: Conversation) => void
}

function formatConversationTitle(conversation: Conversation) {
  return conversation.title.trim() || '未命名会话'
}

export function ConversationSidebar({
  conversations,
  activeConversationID,
  isLoading,
  deletingConversationID,
  onCreate,
  onSelect,
  onDelete,
}: Props) {
  return (
    <aside className="chat-sidebar">
      {/* 品牌标识 */}
      <div className="chat-brand">
        <div className="chat-brand-logo">
          <Sparkles size={16} className="brand-icon" />
        </div>
        <div className="chat-brand-info">
          <span className="brand-name">Memory Agent</span>
          <span className="brand-badge">Agent</span>
        </div>
      </div>

      {/* 新建对话按钮 */}
      <button className="new-chat-button" type="button" onClick={onCreate}>
        <div className="new-chat-content">
          <Plus size={16} className="new-chat-icon" />
          <span>新建对话</span>
        </div>
        <kbd className="new-chat-shortcut">⌘N</kbd>
      </button>

      {/* 历史会话分组头部 */}
      <div className="history-heading">
        <span className="history-title">历史会话</span>
        {!isLoading && conversations.length > 0 && (
          <span className="history-count">{conversations.length}</span>
        )}
      </div>

      {/* 会话列表 */}
      <nav className="conversation-list" aria-label="历史会话">
        {isLoading && (
          <div className="sidebar-skeletons">
            <div className="skeleton-item" />
            <div className="skeleton-item short" />
            <div className="skeleton-item" />
          </div>
        )}

        {!isLoading && conversations.length === 0 && (
          <div className="empty-history-box">
            <MessageSquareDashed size={24} className="empty-history-icon" />
            <p className="empty-history-text">暂无历史会话</p>
            <span className="empty-history-hint">开启探索，沉淀思路</span>
          </div>
        )}

        {!isLoading &&
          conversations.map((conversation) => {
            const isActive = conversation.id === activeConversationID
            const isDeleting = deletingConversationID === conversation.id
            const title = formatConversationTitle(conversation)

            return (
              <div
                className={`conversation-row ${isActive ? 'active' : ''}`}
                key={conversation.id}
              >
                <button
                  className="conversation-item"
                  type="button"
                  title={title}
                  onClick={() => onSelect(conversation.id)}
                >
                  <MessageSquare size={15} className="conversation-item-icon" />
                  <span className="conversation-title-text">{title}</span>
                </button>

                <button
                  className="conversation-delete"
                  type="button"
                  aria-label={`删除会话：${title}`}
                  title="删除此会话"
                  disabled={isDeleting}
                  onClick={(e) => {
                    e.stopPropagation()
                    onDelete(conversation)
                  }}
                >
                  {isDeleting ? (
                    <Loader2 size={13} className="delete-spinner" />
                  ) : (
                    <Trash2 size={13} />
                  )}
                </button>
              </div>
            )
          })}
      </nav>

      {/* 底部工作空间与状态 */}
      <div className="sidebar-account">
        <div className="account-avatar">
          <Bot size={18} />
        </div>
        <div className="account-meta">
          <span className="account-name">本地学习空间</span>
          <span className="account-status">
            <span className="status-dot pulse" />
            工作台就绪
          </span>
        </div>
      </div>
    </aside>
  )
}
