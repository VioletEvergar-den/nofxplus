# 更新说明

## 2026-09-16

### 修复

1. **自定义 System Prompt 实盘不生效**（[trader/auto_trader.go](trader/auto_trader.go)）
   - 交易员设置的自定义提示词此前只保存到数据库，从未拼入实盘决策的 System Prompt
   - 现已正确接入 StrategyEngine，以追加模式（Personalized Trading Strategy 段）附加在系统提示词末尾
   - 修改提示词编辑器、创建/更新交易员、重启恢复三条路径均已生效
   - 注意：为避免与 Prompt Evolution（提示词进化）冲突，暂只支持"追加"模式，完全替换（Override）模式待后续版本

2. **Prompt Lab 百分比显示错误**（[web/src/components/PromptVariantLab.tsx](web/src/components/PromptVariantLab.tsx)）
   - "提示词变体表现"面板的总收益率、胜率、最大回撤此前被重复 ×100（如 5% 显示为 500%）
   - 现已与变体列表卡片口径一致

### 新增

3. **最近决策 · 交易周期筛选**（[api/server.go](api/server.go) / [web/src/App.tsx](web/src/App.tsx)）
   - 「最近决策」面板新增筛选：全部周期 / 仅交易决策
   - 选择「仅交易决策」时只显示 AI 实际执行开仓/平仓动作的周期，过滤纯观望周期
   - 服务端过滤实现，最多回溯最近 500 条决策记录

### 说明

- 「分析」面板与 Prompt Lab 变体指标需要 **≥10 笔已平仓交易** 后才会生成数据，属正常现象
- 已知外部依赖问题：OI Top 公共 API 返回 402（上游 key 失效），与本次更新无关
- 部署：拉取后 `docker compose build && docker compose up -d` 即可
