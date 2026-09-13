# Tasks

## 计划编排

- [x] 为 Step 0 创建独立 OpenSpec change 并完成 proposal、specs、design、tasks 评审。（历史文档未独立拆分，按现有实现记录完成）
- [ ] 为 Step 1 至 Step 9 依次创建独立 OpenSpec change。（待后续补齐独立 change）
- [x] Step 0 至 Step 2 的实现前置接口已完成并经代码验证。

## 实现顺序

- [x] Step 0：初始化 Go/React 工程、健康检查、配置、日志和测试入口。
- [x] Step 1：实现 SQLite 连接、迁移、事务、数据目录和错误协议。
- [x] Step 2：实现题库、作答、复盘、附件、搜索和分页 API。
- [ ] Step 3：实现模型档案、凭据引用、连接测试和端点客户端。（暂缓，后续再做）
- [ ] Step 4：接入 Eino，定义 Agent runtime、工具和确认式记忆保存。（当前阶段）
- [ ] Step 5：实现全文/向量索引、混合检索、引用和索引任务。
- [ ] Step 6：实现 Vercel AI SDK 兼容流式 API、取消和会话记录。
- [ ] Step 7：实现 React/Codex 风格工作区和六个主入口。
- [ ] Step 8：实现分级提示、作答诊断和确认保存流程。
- [ ] Step 9：实现 FSRS、备份恢复、Markdown 导出、性能和发布检查。

## 验收门槛

- [x] Step 0 至 Step 2 的核心验收场景已通过现有 Go 测试和前端构建验证。
- [x] Step 0 至 Step 2 的相关 API 和数据库变更已有测试覆盖。
- [ ] 失败、取消、重试和隐私边界已完成统一验收。（后续步骤继续补齐）
- [ ] 变更归档后再进入下一步骤。（历史 change 尚未归档，需补做流程整理）

## 当前状态

- 已完成：Step 0、Step 1、Step 2。
- 进行中：文档与 OpenSpec change 归档整理。
- 当前阶段：Step 4，Eino Agent、题目记忆检索和确认式记忆保存。
- 暂缓：Step 3 模型配置中心，沿用现有模型配置方式。
