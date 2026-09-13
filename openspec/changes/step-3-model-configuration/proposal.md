# Proposal

## Why

后续 Eino Agent 和 RAG 需要统一、可测试的模型配置边界。本变更为本机单用户应用提供聊天模型与嵌入模型的配置、凭据引用和连接测试能力。

## What Changes

- 增加模型档案及聊天/嵌入用途配置。
- 增加凭据引用抽象，避免 API Key 明文进入业务表和日志。
- 增加模型配置的 REST API 和连接测试接口。
- 提供供 Agent 与 RAG 使用的统一模型客户端接口。

### Non-goals

- 不实现 Eino Agent、RAG 索引或向量数据库。
- 不实现多用户认证和远程凭据管理服务。

## Impact

新增模型配置领域、SQLite 迁移、repository/service/HTTP handler 及测试。

