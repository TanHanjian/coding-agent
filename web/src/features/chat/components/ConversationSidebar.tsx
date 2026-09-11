import type { Conversation } from '../types'

type Props = {
  conversations: Conversation[]
  activeConversationID?: string
  isLoading: boolean
  onCreate: () => void
  onSelect: (conversationID: string) => void
}

function formatConversationTitle(conversation: Conversation) {
  return conversation.title.trim() || '未命名对话'
}

export function ConversationSidebar({
  conversations,
  activeConversationID,
  isLoading,
  onCreate,
  onSelect,
}: Props) {
  return (
    <aside className="chat-sidebar">
      <div className="chat-brand"><span className="chat-brand-mark">M</span><span>Memory Agent</span></div>
      <button className="new-chat-button" type="button" onClick={onCreate}>
        <span className="new-chat-icon">□</span> 新对话
        <span className="new-chat-plus">＋</span>
      </button>

      <div className="history-heading">
        <span>历史会话</span>
        {isLoading && <span className="loading-label">加载中</span>}
      </div>
      <nav className="conversation-list" aria-label="历史会话">
        {conversations.map((conversation) => (
          <button
            className={conversation.id === activeConversationID ? 'conversation-item active' : 'conversation-item'}
            key={conversation.id}
            type="button"
            title={formatConversationTitle(conversation)}
            onClick={() => onSelect(conversation.id)}
          >
            {formatConversationTitle(conversation)}
          </button>
        ))}
        {!isLoading && conversations.length === 0 && <p className="empty-history">还没有会话</p>}
      </nav>

      <div className="sidebar-account"><span className="account-avatar">M</span><span>本地学习空间</span></div>
    </aside>
  )
}
