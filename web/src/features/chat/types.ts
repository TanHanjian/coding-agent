import type { UIMessage } from 'ai'

export type Conversation = {
  id: string
  title: string
  createdAt: string
  updatedAt: string
}

export type MessageStatus = 'streaming' | 'completed' | 'cancelled' | 'failed'

export type PersistedMessage = {
  id: string
  conversationId: string
  sequence: number
  role: 'user' | 'assistant'
  content: string
  status: MessageStatus
  errorCode?: string
  errorMessage?: string
  createdAt: string
  updatedAt: string
}

export function toUIMessage(message: PersistedMessage): UIMessage {
  return {
    id: message.id,
    role: message.role,
    parts: message.content ? [{ type: 'text', text: message.content }] : [],
  }
}
