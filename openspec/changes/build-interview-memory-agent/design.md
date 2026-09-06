# Design

## 架构

系统采用本机单用户 Web 架构。React Web 通过 HTTP 访问 Go 服务；Go 统一负责业务 API、SQLite、Eino Agent、RAG 和本地文件。AI 流式接口输出 Vercel AI SDK 可消费的事件流。

## 模块边界

- `web`：React 页面、路由、状态和 API client。
- `api`：Go HTTP handler、中间件、错误映射和流式响应。
- `question`：题目、作答、复盘、标签和附件业务服务。
- `agent`：Eino runtime、Agent 工具、上下文和确认流程。
- `retrieval`：全文检索、向量检索、索引任务和引用。
- `storage`：SQLite repository、迁移、文件存储和备份。
- `credentials`：模型凭据的安全存储抽象。

## 数据流

普通 CRUD 请求由 React 经 Go API 进入 service 和 repository。AI 请求由 React 发送至 Go Agent API，Go 先执行允许的检索工具，再调用配置的模型端点，最后以流式事件返回文本、引用、工具状态和完成状态。未确认的 AI 输出只能存在于当前会话，不能写入长期记忆。

## 约束

- React 不得包含模型 API Key。
- Agent 工具不得绕过 service 直接写数据库。
- 索引重建使用新索引完成后的原子切换，失败时保留旧索引。
- 所有长任务必须支持状态查询、失败报告和取消语义。

