import type { Conversation, PersistedMessage } from './types'

const apiBase = '/api/v1'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${apiBase}${path}`, init)
  if (!response.ok) {
    const error = await response.json().catch(() => null) as { error?: { message?: string } } | null
    throw new Error(error?.error?.message ?? `请求失败（${response.status}）`)
  }
  return response.json() as Promise<T>
}

export function listConversations() {
  return request<Conversation[]>('/conversations')
}

export function createConversation() {
  return request<Conversation>('/conversations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title: '新对话' }),
  })
}

export function listMessages(conversationID: string) {
  return request<PersistedMessage[]>(`/conversations/${conversationID}/messages`)
}

export function cancelGeneration(assistantMessageID: string) {
  return request<void>(`/chat/${assistantMessageID}/cancel`, { method: 'POST' })
}
