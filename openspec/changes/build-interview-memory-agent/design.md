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

## 运行恢复与可选观测

本机数据目录只允许一个进程独占；占用失败时拒绝第二实例启动。在接受请求前，原子地将遗留 `streaming` 收敛为 `failed`，错误码为 `generation_interrupted`，保留已持久化文本。旧 client id 重放返回旧结果，新 client id 才是重新提交；不实现 checkpoint，不能仅凭 Hub 缺失判定执行失效。

完整 Trace 默认关闭，与脱敏 Trace 独立配置。启用配置不能代替授权：告知接收平台（接收方）、用途和内容范围，用户明确授权后可上报完整真实学习内容，本地记录授权范围（含接收方、用途和内容类别）、授权时间和告知版本；撤销停止后续完整上报（含待发送队列），已上传不自动删除，接收方、用途或内容范围变化重新授权。凭据永不上传，原始错误脱敏。普通日志、诊断和 UI 摘要不是授权 Trace 通道；完整本地备份可包含业务数据但无密钥。

评测保留 [本地 MVP 基线](../add-agent-evaluation/design.md)，固定版本及执行身份约束见该设计；平台接入仅是后续可选项，不改变里程碑范围。

## 约束

- React 不得包含模型 API Key。
- Agent 工具不得绕过 service 直接写数据库。
- 索引重建使用新索引完成后的原子切换，失败时保留旧索引。
- 所有长任务必须支持状态查询、失败报告和取消语义。

