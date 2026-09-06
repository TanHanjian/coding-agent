# Tasks

## 计划编排

- [ ] 为 Step 0 创建独立 OpenSpec change 并完成 proposal、specs、design、tasks 评审。
- [ ] 为 Step 1 至 Step 9 依次创建独立 OpenSpec change。
- [ ] 每个 change 实现前确认前一步的验收和接口契约已完成。

## 实现顺序

- [ ] Step 0：初始化 Go/React 工程、健康检查、配置、日志和测试入口。
- [x] Step 1：实现 SQLite 连接、迁移、事务、数据目录和错误协议。
- [ ] Step 2：实现题库、作答、复盘、附件、搜索和分页 API。
- [ ] Step 3：实现模型档案、凭据引用、连接测试和端点客户端。
- [ ] Step 4：接入 Eino，定义 Agent runtime、工具和确认式记忆保存。
- [ ] Step 5：实现全文/向量索引、混合检索、引用和索引任务。
- [ ] Step 6：实现 Vercel AI SDK 兼容流式 API、取消和会话记录。
- [ ] Step 7：实现 React/Codex 风格工作区和六个主入口。
- [ ] Step 8：实现分级提示、作答诊断和确认保存流程。
- [ ] Step 9：实现 FSRS、备份恢复、Markdown 导出、性能和发布检查。

## 验收门槛

- [ ] 当前步骤的 SDD 验收场景全部通过。
- [ ] 相关 API 和数据库变更有测试覆盖。
- [ ] 失败、取消、重试和隐私边界已验证。
- [ ] 变更归档后再进入下一步骤。
