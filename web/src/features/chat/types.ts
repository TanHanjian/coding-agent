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

export type ChatMessage = UIMessage<{
  status: MessageStatus
  errorCode?: string
}>

export function safeGenerationErrorText(code?: string): string {
  switch (code) {
    case 'context_budget_exceeded':
      return '本次请求的上下文超过模型容量，生成已停止。请缩小请求范围，或使用上下文容量更大的模型。'
    case 'context_counting_failed':
      return '本次请求的上下文无法安全计数，生成已停止。请检查模型和输入配置。'
    default:
      return '生成失败，请重新生成'
  }
}

export function messageFailureText(message: ChatMessage): string | undefined {
  return message.role === 'assistant' && message.metadata?.status === 'failed'
    ? safeGenerationErrorText(message.metadata.errorCode)
    : undefined
}

export function toUIMessage(message: PersistedMessage): ChatMessage {
  return {
    id: message.id,
    role: message.role,
    metadata: { status: message.status, errorCode: message.errorCode },
    parts: message.content ? [{ type: 'text', text: message.content }] : [],
  }
}
