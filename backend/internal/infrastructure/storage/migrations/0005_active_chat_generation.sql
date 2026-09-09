-- 同一 Conversation 同时只能有一条 streaming assistant Message。应用层会先
-- 返回友好的冲突错误；该索引用于兜住并发请求造成的竞争窗口。
CREATE UNIQUE INDEX idx_messages_one_streaming_assistant_per_conversation
    ON messages(conversation_id)
    WHERE role = 'assistant' AND status = 'streaming';
