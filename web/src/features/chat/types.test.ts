/// <reference types="node" />
import assert from 'node:assert/strict'
import test from 'node:test'
import { messageFailureText, toUIMessage } from './types.ts'
import type { PersistedMessage } from './types.ts'

const persisted: PersistedMessage = {
  id: 'a1', conversationId: 'c1', sequence: 2, role: 'assistant',
  content: '已经生成的部分回答', status: 'failed',
  createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
  errorMessage: 'private provider prompt, tool arguments and API key',
}

for (const [code, expected] of [
  ['context_budget_exceeded', '本次请求的上下文超过模型容量，生成已停止。请缩小请求范围，或使用上下文容量更大的模型。'],
  ['context_counting_failed', '本次请求的上下文无法安全计数，生成已停止。请检查模型和输入配置。'],
  ['model_failed', '生成失败，请重新生成'],
  ['private unknown diagnostic', '生成失败，请重新生成'],
  [undefined, '生成失败，请重新生成'],
] as const) {
  test(`history failure uses safe mapping for ${code}`, () => {
    const message = toUIMessage({ ...persisted, errorCode: code })
    assert.deepEqual(message.parts, [{ type: 'text', text: persisted.content }])
    assert.equal(message.metadata?.errorCode, code)
    assert.equal(messageFailureText(message), expected)
    assert.equal(JSON.stringify(message).includes(persisted.errorMessage!), false)
    // Errors stay outside assistant text even when generation produced nothing.
    const empty = toUIMessage({ ...persisted, content: '', errorCode: code })
    assert.deepEqual(empty.parts, [])
    assert.equal(messageFailureText(empty), expected)
  })
}

for (const status of ['streaming', 'completed', 'cancelled'] as const) {
  test(`history ${status} is not rendered as a failure`, () => {
    assert.equal(messageFailureText(toUIMessage({ ...persisted, status, errorCode: 'context_budget_exceeded' })), undefined)
  })
}

test('user messages do not display generation failures', () => {
  assert.equal(messageFailureText(toUIMessage({ ...persisted, role: 'user', errorCode: 'context_budget_exceeded' })), undefined)
})
