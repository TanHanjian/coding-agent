# Tasks

- [x] 定义版本化 JSONL schema 与 24 条首批 case。
- [x] 实现 fixture question service 和工具调用 recorder。
- [x] 实现 100 分制确定性 scorer 与关键失败规则。
- [x] 实现可选独立 LLM Judge 与 JSON 重试校验。
- [x] 实现 live runner、CLI 过滤、重复运行和超时。
- [x] 实现 JSON/Markdown 报告。
- [x] 增加 dataset、fixture、scorer 和 report 单元测试。
- [ ] 实现 case/run/execution 身份分离，记录 `run_id`、`case_id`、`case_version`、`repeat_index`，确保上传重传复用原执行身份，独立新评测生成新 run_id，repeat 不互相覆盖。
- [ ] 实现固定 Prompt 版本解析与同工作空间、Prompt key、具体版本的完整性校验缓存；缺失、哈希不匹配和解析失败记录为基础设施失败，不计质量分且不允许剔除后判发布通过。
- [ ] 区分在线聊天可回退与 live eval 禁止本地 fallback 的策略，并在评测报告记录 requested/resolved/source/fallback/content hash、有效/无效数量、基础设施失败数量和发布资格。
- [ ] 保持本地 MVP 不依赖第三方评测平台；补充平台字段映射验证前的可选扩展说明。
- [ ] 在受信任 CI 任务中接入 `validate` 和 smoke report 上传。
- [ ] 依据人工标注校准 Judge 阈值，再决定是否升级为合并门禁。
- [ ] 增加固定 Prompt 版本失败隔离、在线聊天回退、缓存边界、发布资格以及重传/repeat/新运行身份测试。
