# Model Configuration

## Requirements

### Requirement: Manage model profiles
系统 MUST 支持创建、读取、更新和删除聊天模型或嵌入模型档案，并校验 provider、model、用途和端点配置。

#### Scenario: Create valid profile
- **WHEN** 客户端提交有效模型档案
- **THEN** 系统持久化档案并返回不含密钥的模型信息

#### Scenario: Reject invalid profile
- **WHEN** provider、model 或用途缺失或不合法
- **THEN** 系统返回 `invalid_request` 且不写入数据库

### Requirement: Protect credentials
系统 MUST 只保存凭据引用，且 MUST NOT 在 API 响应、日志或错误中输出凭据内容。

#### Scenario: Save credential reference
- **WHEN** 档案包含凭据引用
- **THEN** 系统保存引用并在读取时返回脱敏状态

### Requirement: Test connectivity
系统 MUST 支持对指定档案执行带超时的连接测试，并返回成功或可分类的失败结果。

#### Scenario: Connectivity timeout
- **WHEN** provider 在规定时间内无响应
- **THEN** 系统取消测试并返回连接超时错误，不修改档案

