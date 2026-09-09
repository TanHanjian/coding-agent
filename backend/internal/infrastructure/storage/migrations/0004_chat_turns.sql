-- 为 UIMessage 的客户端幂等键预留持久化列。空字符串表示普通 Message CRUD
-- 创建的历史消息；只有聊天入口写入非空 client_message_id。
ALTER TABLE messages ADD COLUMN client_message_id TEXT NOT NULL DEFAULT '';

-- 同一会话内，同一前端消息只能启动一次生成；不同会话可使用相同前端 ID。
CREATE UNIQUE INDEX idx_messages_conversation_client_message_id
    ON messages(conversation_id, client_message_id)
    WHERE client_message_id <> '';
