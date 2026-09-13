# Design

## 模块

- `domain/modelconfig`：模型档案、用途、校验和错误。
- `infrastructure/repository/sqlite`：模型档案和端点配置持久化。
- `application/modelconfig`：凭据解析、连接测试和客户端工厂。
- `transport/httpmodelconfig`：配置 CRUD 与连接测试 API。

## 数据与安全

SQLite 保存 provider、model、base URL、用途和凭据引用，不保存 API Key 明文。凭据通过进程配置或本机凭据适配器解析；响应和日志只返回脱敏信息。

## 接口

提供模型档案列表、创建、更新、删除和连接测试接口。连接测试使用请求上下文和超时，返回可观察的成功或分类失败结果。

