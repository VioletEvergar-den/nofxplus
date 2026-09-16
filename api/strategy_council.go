package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nofx/logger"
	"nofx/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================
// AI 策略专家团会诊（Strategy AI Council）
// 流程：情报规划(SearXNG) → 情报收集(并行) → 策略制定(串行接力) → 审核定稿 → 程序格式审核环
// ============================================================

// ---------- 状态与类型 ----------

type councilStepStatus string

const (
	councilStepPending councilStepStatus = "pending"
	councilStepRunning councilStepStatus = "running"
	councilStepDone    councilStepStatus = "done"
	councilStepFailed  councilStepStatus = "failed"
	councilStepSkipped councilStepStatus = "skipped"
)

// councilStep 单个角色的执行状态（用于前端过程展示）
type councilStep struct {
	Role        string   `json:"role"`
	Emoji       string   `json:"emoji"`
	Status      string   `json:"status"`
	Summary     string   `json:"summary,omitempty"`
	Concerns    []string `json:"concerns,omitempty"`
	Payload     any      `json:"payload,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Activity    []string `json:"activity,omitempty"` // Agent 动作流水（工具调用/追问/发言）
	Error       string   `json:"error,omitempty"`
	DurationMs  int64    `json:"duration_ms"`
	Round       int      `json:"round"` // 1研判 2制定 3定稿
}

// councilTranscriptEntry 圆桌对话流条目（Agent 间的消息传递记录）
type councilTranscriptEntry struct {
	Role      string    `json:"role"`
	Emoji     string    `json:"emoji"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // speech|tool_call|tool_result|question|answer|system
	Content   string    `json:"content"`
	Payload   any       `json:"payload,omitempty"` // speech/answer 附带的结构化产出（前端折叠展示）
	CreatedAt time.Time `json:"created_at"`
}

// councilFinalResult 会诊最终结果
type councilFinalResult struct {
	Config                 *store.StrategyConfig `json:"config"`
	ScanIntervalSuggestion int                   `json:"scan_interval_suggestion"`
	Reasoning              string                `json:"reasoning"`
	ClampWarnings          []string              `json:"clamp_warnings"`
	RepairRounds           int                   `json:"repair_rounds"`
}

// councilState 一次会诊的完整状态
type councilState struct {
	ID         string              `json:"id"`
	UserID     string              `json:"user_id"`
	Status     string              `json:"status"` // running|completed|failed|cancelled
	Mode       string              `json:"mode"`   // generate|modify
	Intent     string              `json:"intent"`
	Language   string              `json:"language"`
	SearchOn   bool                `json:"search_on"`
	Budget     int                 `json:"budget"`      // Agent 全场调用总预算（用户可设）
	UsedBudget int32               `json:"used_budget"` // 已消耗调用次数（atomic）
	Steps      []*councilStep      `json:"steps"`
	Transcript []councilTranscriptEntry `json:"transcript,omitempty"` // 圆桌对话流
	Result     *councilFinalResult `json:"result,omitempty"`
	Error      string              `json:"error,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
	cancelFlag atomic.Bool         `json:"-"`

	// mu 保护 Steps/Status/Result/Error 的并发读写（runCouncil goroutine 写，GET handler 读）
	mu sync.RWMutex `json:"-"`
}

// setStatus 更新会诊状态（并发安全）
func (st *councilState) setStatus(status, errMsg string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Status = status
	st.Error = errMsg
}

// setResult 写入最终结果（并发安全）
func (st *councilState) setResult(result *councilFinalResult) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Result = result
}

var (
	councilStates   = map[string]*councilState{}
	councilStatesMu sync.RWMutex
)

// councilRoleDef 角色定义
type councilRoleDef struct {
	ID     string
	Emoji  string
	NameZH string
	NameEN string
	Round  int
}

// 5 个 Agent 角色（圆桌串行，前序发言全量可见，可自主调用工具）
var councilRoles = []councilRoleDef{
	{ID: "intel_analyst", Emoji: "🔍", NameZH: "情报分析师", NameEN: "Intel Analyst", Round: 1},
	{ID: "chief_trader", Emoji: "🎯", NameZH: "首席合约交易员", NameEN: "Chief Trader", Round: 1},
	{ID: "strategy_architect", Emoji: "🏗️", NameZH: "策略架构师", NameEN: "Strategy Architect", Round: 2},
	{ID: "risk_reviewer", Emoji: "⚖️", NameZH: "风控评审官", NameEN: "Risk Reviewer", Round: 2},
	{ID: "prompt_writer", Emoji: "✍️", NameZH: "首席策略撰写官", NameEN: "Chief Prompt Writer", Round: 3},
}

// ---------- 统一信封 ----------

// councilEnvelope 每个角色输出的统一信封
type councilEnvelope struct {
	Summary    string         `json:"summary"`
	Payload    map[string]any `json:"payload"`
	Concerns   []string       `json:"concerns"`
	Confidence int            `json:"confidence"`
}

const envelopeSchemaHint = `{
  "summary": "给用户看的一句话结论（用 %s）",
  "payload": { ...本角色的结构化产出，见下方字段要求... },
  "concerns": ["对上游结论的疑虑或修正建议，没有则空数组"],
  "confidence": 0-100
}`

// extractJSONBlock 从 AI 输出中提取 JSON（兼容 ```json 代码块与裸 JSON）
func extractJSONBlock(text string) (string, bool) {
	// 优先匹配 ```json ... ```
	if idx := strings.Index(text, "```json"); idx >= 0 {
		rest := text[idx+len("```json"):]
		if end := strings.LastIndex(rest, "```"); end > 0 {
			return strings.TrimSpace(rest[:end]), true
		}
	}
	if idx := strings.Index(text, "```"); idx >= 0 {
		rest := text[idx+len("```"):]
		if end := strings.LastIndex(rest, "```"); end > 0 {
			candidate := strings.TrimSpace(rest[:end])
			if strings.HasPrefix(candidate, "{") {
				return candidate, true
			}
		}
	}
	// 裸 JSON：第一个 { 到最后一个 }
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1], true
	}
	return "", false
}

// parseEnvelope 解析角色输出的统一信封
func parseEnvelope(resp string) (*councilEnvelope, error) {
	block, ok := extractJSONBlock(resp)
	if !ok {
		return nil, fmt.Errorf("未在 AI 输出中找到 JSON")
	}
	var env councilEnvelope
	if err := json.Unmarshal([]byte(block), &env); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if env.Payload == nil {
		// 兼容修复：AI 可能漏写 payload 包装，把结构化内容直接放在信封顶层——自动抢救
		var raw map[string]any
		if err := json.Unmarshal([]byte(block), &raw); err == nil {
			extra := make(map[string]any)
			for k, v := range raw {
				if k == "summary" || k == "concerns" || k == "confidence" {
					continue
				}
				extra[k] = v
			}
			if len(extra) > 0 {
				env.Payload = extra
			}
		}
	}
	if env.Payload == nil {
		return nil, fmt.Errorf("信封缺少 payload 字段（须包含本角色的结构化产出）")
	}
	return &env, nil
}

// ---------- 角色任务定义 ----------

// councilTask 单角色的 system prompt 与 payload 要求
func councilTaskPrompt(role councilRoleDef, lang string) string {
	summaryLang := "中文"
	if lang == "en" {
		summaryLang = "English"
	}
	base := fmt.Sprintf(`你是 NOFX 量化交易系统「策略专家团」中的成员。整个团队正在为用户协作生成/修改一套加密货币合约策略配置。

## 输出要求（严格遵守）
只输出一个 JSON 对象（信封格式），不要输出任何其他文字：
%s

- summary 和 concerns 用 %s。
- payload 内的字段名和枚举值必须严格使用下方给定的英文值。
- 对上游结论有不同意见时必须写入 concerns，供风控官和首席评审裁决。
`, envelopeSchemaHint, summaryLang)

	switch role.ID {
	case "intel_planner":
		return base + `
## 你的任务（情报规划师）
根据用户意图，规划联网搜索的关键词（供 SearXNG 检索最新市场资讯）。

payload 字段：
- market_queries: string[]，宏观市场搜索词，1~3 个（如 "BTC 大盘 走势", "crypto market news today"）
- coin_queries: string[]，具体币种搜索词，1~3 个（如 "ETH ETF news"）
- 若用户意图中提到具体币种，优先为这些币种构造搜索词；否则围绕 BTC/ETH 主流币与大盘。

只输出信封 JSON。`
	case "market_analyst":
		return base + `
## 你的任务（宏观分析师）
基于用户意图和「市场情报简报」（如有），判断当前加密货币市场状态。

payload 字段：
- market_regime: "trending_up" | "trending_down" | "ranging" | "volatile" | "extreme"
- bias: "bullish" | "bearish" | "neutral"
- key_points: string[]，2~4 条关键判断依据
- 情报简报缺失时基于模型知识判断，并在 concerns 中注明"无联网情报，置信度已下调"。`
	case "coin_researcher":
		return base + `
## 你的任务（币种研究员）
基于用户意图和「币种情报简报」（如有），给出用户关注币种的消息面画像。

payload 字段：
- coin_profiles: object[]，每个元素: {"symbol": "ETHUSDT", "sentiment": "bullish|bearish|neutral", "catalysts": "近期催化剂概述", "volatility": "high|medium|low", "notes": "一句话点评"}
- suggested_coins: string[]，基于情报推荐关注的币种（带 USDT 后缀，最多 5 个）
- 用户意图未提及币种时给出当前市场最值得关注的主流币种。`
	case "chief_trader":
		return base + `
## 你的任务（首席合约交易员）
你是身经百战的加密货币合约交易员：穿越多轮牛熊，经历过 312、519 级别的极端行情，纪律第一、生存第一，对杠杆始终心存敬畏。你深知大部分时间应该等待而不是交易。

基于上游情报，给出实战交易计划。

payload 字段：
- market_view: 一句话盘面观点
- direction_preference: "long" | "short" | "both" | "neutral"
- expected_holding: 预期持仓周期 "scalp"(分钟级) | "intraday"(数小时) | "swing"(1~3天) | "position"(数天以上)
- entry_logic: 什么条件下入场
- no_trade_conditions: string[]，什么情况下坚决不做单（震荡收窄/消息不明朗/波动异常等，至少 2 条）
- leverage_attitude: "conservative" | "moderate" | "aggressive"（实战视角对杠杆的真实态度，通常不超过 moderate）`
	case "strategy_architect":
		return base + `
## 你的任务（策略架构师）
基于交易计划和情报，确定策略的交易模式与整体思路。

payload 字段：
- trading_mode: "balanced"(均衡) | "aggressive"(激进) | "conservative"(保守) | "scalping"(超短线)
- approach: 2~3 句话的整体策略思路
- 必须与交易员的 expected_holding 和 leverage_attitude 保持一致，冲突时写入 concerns。`
	case "timeframe_engineer":
		return base + `
## 你的任务（周期指标工程师）
你会收到「多周期真实K线数据」：周K线、日K线、4小时K线三个周期，各自独立一节（区间统计、EMA/RSI/MACD/ATR 快照、最近5根K线）。

分析要求：
- 必须对周K、日K、4小时K 每个时间区间分别给出独立趋势判断（不许混在一起笼统描述），并标注各周期之间的共振或矛盾。
- 基于预期持仓周期，把真实多周期走势翻译成 K 线时间周期组合与技术指标配置。

合法时间周期（只能从中选择）: "1m", "3m", "5m", "15m", "30m", "1h", "4h", "1d"
持仓周期 → 参考组合: scalp→1m/3m/5m 主周期; intraday→5m/15m/1h; swing→15m/1h/4h; position→1h/4h/1d

payload 字段：
- multi_tf_views: object[]，三个周期每个都必须有: {"timeframe": "1w|1d|4h", "trend": "up|down|range", "key_evidence": "该周期关键证据（EMA排列/RSI/MACD/价格结构，1~2句）"}
- primary_timeframe: 主周期（上述枚举之一）
- primary_count: 主周期K线数量，10~500（通常 30~100）
- enable_multi_timeframe: boolean
- selected_timeframes: string[]，多周期组合（2~4 个，从合法枚举中选）
- longer_timeframe: 更长确认周期（可选，同枚举）
- longer_count: number（可选，10~100）
- indicators: object:
  - enable_ema: boolean, ema_periods: number[]（如 [20,50]，周期 2~200）
  - enable_rsi: boolean, rsi_periods: number[]（如 [7,14]）
  - enable_macd: boolean, macd_fast_period: number, macd_slow_period: number
  - enable_atr: boolean, atr_periods: number[]（如 [14]）
  - enable_boll: boolean, boll_periods: number[]（如 [20]）
  - enable_volume: boolean, enable_oi: boolean, enable_funding_rate: boolean
- rationale: 选择理由（1~2 句，需引用多周期判断结论）
- 短线策略必须有 EMA/RSI 类快指标；波段以上建议 MACD/BOLL；OI 与成交量建议常开。`
	case "coin_planner":
		return base + `
## 你的任务（币种来源规划师）
基于情报与交易风格，规划策略的币种来源。

payload 字段：
- source_type: "static"(固定币种列表) | "coinpool"(AI500 动态池) | "oi_top"(OI 排名) | "mixed"(混合)
- static_coins: string[]，source_type 为 static/mixed 时提供（带 USDT 后缀，最多 10 个）
- coin_pool_limit: number（coinpool/mixed 时，5~30）
- oi_top_limit: number（oi_top/mixed 时，5~50）
- rationale: 1~2 句理由
- 短线高频风格适合动态池（coinpool/mixed），专注研究某几个币用 static。`
	case "risk_officer":
		return base + `
## 你的任务（风控官）
你是独立第三方风控官，负责两件事：给出风控参数；对上游策略层（交易员/架构师/周期工程师/币种规划师）进行独立审查。

payload 字段：
- risk_control: object:
  - max_positions: 同时最大持仓数（1~10）
  - btc_eth_max_leverage: BTC/ETH 最大杠杆（1~20，实战建议 ≤10）
  - altcoin_max_leverage: 山寨币最大杠杆（1~20，实战建议 ≤5）
  - btc_eth_max_position_value_ratio: BTC/ETH 单仓价值/权益上限（0.1~20，默认 5）
  - altcoin_max_position_value_ratio: 山寨币单仓价值/权益上限（0.1~20，默认 1）
  - max_margin_usage: 最大保证金使用率 0.1~1.0（建议 ≤0.9）
  - min_position_size: 单仓最小金额 USDT（建议 12）
  - min_risk_reward_ratio: 最小盈亏比（建议 3）
  - min_confidence: 最低开仓信心分 50~99（建议 75）
- scan_interval_minutes: 扫描周期建议（分钟）：scalp→1~3, intraday→5~15, swing→15~60, position→60~240
- risk_review: object: {"verdict": "pass"|"warning"|"reject", "comments": string[], "objections": [{"target_role": "角色ID", "issue": "问题", "suggestion": "修改建议"}]}
- rationale: 1~2 句理由
- 审查必须对照交易员的 no_trade_conditions 和 leverage_attitude：架构激进而交易员保守时要提出 objection。`
	case "chief_reviewer":
		return base + `
## 你的任务（首席评审）
你是专家团主席：裁决全部 concerns 与 objections，合并各角色产出，输出最终策略配置终稿。

payload 字段：
- final_config: object（终稿，字段与枚举必须严格遵守）:
  - trading_mode: "balanced"|"aggressive"|"conservative"|"scalping"
  - coin_source: {"source_type": "static"|"coinpool"|"oi_top"|"mixed", "static_coins": string[], "coin_pool_limit": number, "oi_top_limit": number}
  - klines: {"primary_timeframe": "1m|3m|5m|15m|30m|1h|4h|1d", "primary_count": number, "enable_multi_timeframe": boolean, "selected_timeframes": string[], "longer_timeframe": string, "longer_count": number}
  - indicators: {"enable_ema": bool, "ema_periods": number[], "enable_rsi": bool, "rsi_periods": number[], "enable_macd": bool, "macd_fast_period": number, "macd_slow_period": number, "enable_atr": bool, "atr_periods": number[], "enable_boll": bool, "boll_periods": number[], "enable_volume": bool, "enable_oi": bool, "enable_funding_rate": bool}
  - risk_control: {"max_positions": n, "btc_eth_max_leverage": n, "altcoin_max_leverage": n, "btc_eth_max_position_value_ratio": f, "altcoin_max_position_value_ratio": f, "max_margin_usage": f, "min_position_size": f, "min_risk_reward_ratio": f, "min_confidence": n}
- scan_interval_minutes: 扫描周期建议（分钟，1~1440）
- reasoning: 终审总结（做了哪些裁决、为什么）
- verdicts: object[]，对每条 concern/objection 的裁决: {"concern_from": "角色ID", "decision": "accepted"|"rejected", "reason": "理由"}
- 裁决原则：安全性优先于收益性；交易员的 no_trade_conditions 与风控官的 objections 除非有强理由否则采纳。`
	case "prompt_writer":
		pw := base + `
## 你的任务（首席策略撰写官）
你是专家团的首席笔杆子：基于全部上游备忘录（交易计划/策略架构/周期指标/币种来源/风控审查/终审裁决），为交易系统撰写一套完整、可执行的 System Prompt 策略。这份提示词将直接作为 AI 交易员的行为准则，质量标准是「拿来即可实盘」。

写作要求（必须遵守）：
1. 风格对标真实实战策略提示词：有清晰的人设身份与方法论体系（如维科夫+SMC+缠论融合、马丁网格、动量突破等，依据策略意图选定）、具体的入场信号条件、明确的执行规则、逐条列出的禁忌清单。
2. 必须具体可执行：写明明确的交易执行数字——信心分阈值、止损止盈距离或结构位规则、分批加减仓规则、持仓时长预期、移动止损规则等。拒绝空泛套话（如"注意风险""谨慎交易"这类没有操作含义的句子）。
3. 严禁写死与参数配置冲突的系统硬约束：最大同时持仓数、交易所杠杆上限、保证金使用率、单仓价值比例等由程序动态注入，提示词中不要重复定义这些全局数字。
4. 四段结构：
   - role_definition（角色定义）：人设身份 + 方法论体系 + 核心目标。核心目标必须包含：最大化夏普比率（平均回报/回报波动），纪律优先于利润。
   - trading_frequency（交易频率理念）：多久评估交易一次、什么情况必须空仓等待、持仓周期预期（按策略类型给具体时长范围）。
   - entry_standards（入场标准）：具体的信号组合条件（形态/指标/资金流/结构位满足什么才允许进场）、分批建仓规则、明确列出禁止入场的情形。
   - decision_process（决策流程）：从「检查现有持仓（止盈止损判断）→ 分析候选币种 → 输出决策」的完整步骤，含每步的执行纪律与思维链要求。
5. custom_prompt（可选）：额外的禁忌清单、风格化要求或特色纪律；没有则输出空字符串 ""。
6. 全文用 {{LANG}} 书写，语气坚定、指令明确、面向执行者。

payload 字段：
- prompt_sections: {"role_definition": string, "trading_frequency": string, "entry_standards": string, "decision_process": string}（四段均必填）
- custom_prompt: string（可为 ""）
- style_note: 一句话说明本策略的风格定位与目标行情`
		return strings.Replace(pw, "{{LANG}}", summaryLang, 1)
	}
	return base
}

// ---------- 补丁合并与程序审核 ----------

// extractWriterSections 从撰写官 payload 中提取并校验 System Prompt 四段
// 返回 (四段文本map, 错误列表)；错误非空表示需要打回重写
func extractWriterSections(payload map[string]any) (map[string]string, []string) {
	var errs []string
	raw, ok := payload["prompt_sections"].(map[string]any)
	if !ok {
		return nil, []string{"缺少 prompt_sections 对象"}
	}
	get := func(key string) string {
		s, _ := raw[key].(string)
		return strings.TrimSpace(s)
	}
	role := get("role_definition")
	freq := get("trading_frequency")
	entry := get("entry_standards")
	process := get("decision_process")

	if len([]rune(role)) < 60 {
		errs = append(errs, "role_definition 过短（至少 60 字，需含人设与方法论体系）")
	}
	if len([]rune(freq)) < 30 {
		errs = append(errs, "trading_frequency 过短（至少 30 字）")
	}
	if len([]rune(entry)) < 80 {
		errs = append(errs, "entry_standards 过短（至少 80 字，需含具体信号条件与禁止入场情形）")
	}
	if len([]rune(process)) < 40 {
		errs = append(errs, "decision_process 过短（至少 40 字，需含完整决策步骤）")
	}
	for name, txt := range map[string]string{
		"role_definition": role, "trading_frequency": freq,
		"entry_standards": entry, "decision_process": process,
	} {
		if len([]rune(txt)) > 6000 {
			errs = append(errs, name+" 超长（上限 6000 字）")
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return map[string]string{
		"role_definition": role, "trading_frequency": freq,
		"entry_standards": entry, "decision_process": process,
	}, nil
}


var validTimeframes = map[string]bool{
	"1m": true, "3m": true, "5m": true, "15m": true,
	"30m": true, "1h": true, "4h": true, "1d": true,
}

var validSourceTypes = map[string]bool{"static": true, "coinpool": true, "oi_top": true, "mixed": true}

var validTradingModes = map[string]bool{"balanced": true, "aggressive": true, "conservative": true, "scalping": true}

var coinSymbolPattern = regexp.MustCompile(`^[A-Z0-9]{2,12}(USDT|USD|PERP)?$`)

// clampInt 从 patch map 取 int 并钳制
func clampInt(m map[string]any, key string, min, max int, warnings *[]string, label string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	n, err := toInt(v)
	if err != nil {
		return 0, false
	}
	orig := n
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	if n != orig {
		*warnings = append(*warnings, fmt.Sprintf("%s 已从 %d 钳制为 %d", label, orig, n))
	}
	return n, true
}

// clampFloat 从 patch map 取 float64 并钳制
func clampFloat(m map[string]any, key string, min, max float64, warnings *[]string, label string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, err := toFloat(v)
	if err != nil {
		return 0, false
	}
	orig := f
	if f < min {
		f = min
	}
	if f > max {
		f = max
	}
	if f != orig {
		*warnings = append(*warnings, fmt.Sprintf("%s 已从 %.2f 钳制为 %.2f", label, orig, f))
	}
	return f, true
}

// clampBool 从 patch map 取 bool
func clampBool(m map[string]any, key string) (bool, bool) {
	v, ok := m[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func toInt(v any) (int, error) {
	switch t := v.(type) {
	case float64:
		return int(t), nil
	case int:
		return t, nil
	case string:
		return strconv.Atoi(strings.TrimSpace(t))
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case int:
		return float64(t), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(t), 64)
	default:
		return 0, fmt.Errorf("not a number")
	}
}

// clampPeriodList 钳制周期数组（每项 2~200，最多 5 个）
func clampPeriodList(v any, warnings *[]string, label string) ([]int, bool) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	if len(arr) > 5 {
		arr = arr[:5]
		*warnings = append(*warnings, fmt.Sprintf("%s 周期数量超过 5 个，已截断", label))
	}
	var out []int
	for _, item := range arr {
		n, err := toInt(item)
		if err != nil {
			continue
		}
		orig := n
		if n < 2 {
			n = 2
		}
		if n > 200 {
			n = 200
		}
		if n != orig {
			*warnings = append(*warnings, fmt.Sprintf("%s 周期 %d 已钳制为 %d", label, orig, n))
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

// clampTimeframeList 过滤非法周期、去重、限制 4 个
func clampTimeframeList(v any, warnings *[]string, label string) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(strings.ToLower(s))
		if !validTimeframes[s] {
			*warnings = append(*warnings, fmt.Sprintf("%s 含非法周期 %q，已忽略", label, s))
			continue
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) > 4 {
		out = out[:4]
		*warnings = append(*warnings, fmt.Sprintf("%s 最多保留 4 个周期", label))
	}
	return out, len(out) > 0
}

// mergeCouncilConfig 将首席评审的终稿合并到 base 配置（程序硬审核 + 钳制）
func mergeCouncilConfig(base *store.StrategyConfig, payload map[string]any, warnings *[]string) {
	// trading_mode
	if tm, ok := payload["trading_mode"].(string); ok && validTradingModes[strings.ToLower(tm)] {
		base.TradingMode = strings.ToLower(tm)
		// 模式变化时同步刷新内置提示词段（用户自定义段会被 PromptSectionsMatchTemplate 保护）
		base.SetConfigPromptSectionsByModeAndLang(base.TradingMode, "zh")
	}

	// coin_source
	if cs, ok := payload["coin_source"].(map[string]any); ok {
		if st, ok := cs["source_type"].(string); ok {
			st = strings.ToLower(strings.TrimSpace(st))
			if validSourceTypes[st] {
				base.CoinSource.SourceType = st
				base.CoinSource.UseCoinPool = st == "coinpool" || st == "mixed"
				base.CoinSource.UseOITop = st == "oi_top" || st == "mixed"
			} else {
				*warnings = append(*warnings, fmt.Sprintf("币种来源类型 %q 非法，保留原值", st))
			}
		}
		if coins, ok := cs["static_coins"].([]any); ok {
			var cleaned []string
			seen := map[string]bool{}
			for _, item := range coins {
				s, ok := item.(string)
				if !ok {
					continue
				}
				s = strings.ToUpper(strings.TrimSpace(s))
				if s == "" {
					continue
				}
				if !strings.HasSuffix(s, "USDT") && !strings.HasSuffix(s, "USD") && !strings.HasSuffix(s, "PERP") {
					s += "USDT"
				}
				if !coinSymbolPattern.MatchString(s) || seen[s] {
					continue
				}
				seen[s] = true
				cleaned = append(cleaned, s)
				if len(cleaned) >= 10 {
					break
				}
			}
			if len(cleaned) > 0 {
				base.CoinSource.StaticCoins = cleaned
			} else {
				*warnings = append(*warnings, "AI 未提供有效的固定币种列表，保留原值")
			}
		}
		if n, ok := clampInt(cs, "coin_pool_limit", 5, 30, warnings, "AI500 池上限"); ok {
			base.CoinSource.CoinPoolLimit = n
		}
		if n, ok := clampInt(cs, "oi_top_limit", 5, 50, warnings, "OI Top 上限"); ok {
			base.CoinSource.OITopLimit = n
		}
	}

	// klines
	if kl, ok := payload["klines"].(map[string]any); ok {
		if tf, ok := kl["primary_timeframe"].(string); ok {
			tf = strings.TrimSpace(strings.ToLower(tf))
			if validTimeframes[tf] {
				base.Indicators.Klines.PrimaryTimeframe = tf
			} else {
				*warnings = append(*warnings, fmt.Sprintf("主周期 %q 非法，保留原值", tf))
			}
		}
		if n, ok := clampInt(kl, "primary_count", 10, 500, warnings, "主周期K线数量"); ok {
			base.Indicators.Klines.PrimaryCount = n
		}
		if b, ok := clampBool(kl, "enable_multi_timeframe"); ok {
			base.Indicators.Klines.EnableMultiTimeframe = b
		}
		if tfs, ok := clampTimeframeList(kl["selected_timeframes"], warnings, "多周期组合"); ok {
			base.Indicators.Klines.SelectedTimeframes = tfs
			// 主周期必须在组合内
			found := false
			for _, tf := range tfs {
				if tf == base.Indicators.Klines.PrimaryTimeframe {
					found = true
					break
				}
			}
			if !found {
				base.Indicators.Klines.PrimaryTimeframe = tfs[0]
				*warnings = append(*warnings, "主周期不在多周期组合内，已调整为组合第一项")
			}
		}
		if tf, ok := kl["longer_timeframe"].(string); ok {
			tf = strings.TrimSpace(strings.ToLower(tf))
			if validTimeframes[tf] {
				base.Indicators.Klines.LongerTimeframe = tf
			}
		}
		if n, ok := clampInt(kl, "longer_count", 10, 500, warnings, "长周期K线数量"); ok {
			base.Indicators.Klines.LongerCount = n
		}
	}

	// indicators
	if ind, ok := payload["indicators"].(map[string]any); ok {
		if b, ok := clampBool(ind, "enable_ema"); ok {
			base.Indicators.EnableEMA = b
		}
		if p, ok := clampPeriodList(ind["ema_periods"], warnings, "EMA"); ok {
			base.Indicators.EMAPeriods = p
		}
		if b, ok := clampBool(ind, "enable_rsi"); ok {
			base.Indicators.EnableRSI = b
		}
		if p, ok := clampPeriodList(ind["rsi_periods"], warnings, "RSI"); ok {
			base.Indicators.RSIPeriods = p
		}
		if b, ok := clampBool(ind, "enable_macd"); ok {
			base.Indicators.EnableMACD = b
		}
		if n, ok := clampInt(ind, "macd_fast_period", 2, 50, warnings, "MACD 快线"); ok {
			base.Indicators.MACDFastPeriod = n
		}
		if n, ok := clampInt(ind, "macd_slow_period", 5, 100, warnings, "MACD 慢线"); ok {
			base.Indicators.MACDSlowPeriod = n
		}
		if base.Indicators.MACDFastPeriod >= base.Indicators.MACDSlowPeriod {
			base.Indicators.MACDFastPeriod, base.Indicators.MACDSlowPeriod = base.Indicators.MACDSlowPeriod, base.Indicators.MACDFastPeriod
			*warnings = append(*warnings, "MACD 快线周期大于慢线，已自动交换")
		}
		if b, ok := clampBool(ind, "enable_atr"); ok {
			base.Indicators.EnableATR = b
		}
		if p, ok := clampPeriodList(ind["atr_periods"], warnings, "ATR"); ok {
			base.Indicators.ATRPeriods = p
		}
		if b, ok := clampBool(ind, "enable_boll"); ok {
			base.Indicators.EnableBOLL = b
		}
		if p, ok := clampPeriodList(ind["boll_periods"], warnings, "BOLL"); ok {
			base.Indicators.BOLLPeriods = p
		}
		if b, ok := clampBool(ind, "enable_volume"); ok {
			base.Indicators.EnableVolume = b
		}
		if b, ok := clampBool(ind, "enable_oi"); ok {
			base.Indicators.EnableOI = b
		}
		if b, ok := clampBool(ind, "enable_funding_rate"); ok {
			base.Indicators.EnableFundingRate = b
		}
	}

	// risk_control
	if rc, ok := payload["risk_control"].(map[string]any); ok {
		if n, ok := clampInt(rc, "max_positions", 1, 10, warnings, "最大持仓数"); ok {
			base.RiskControl.MaxPositions = n
		}
		if n, ok := clampInt(rc, "btc_eth_max_leverage", 1, 20, warnings, "BTC/ETH 杠杆上限"); ok {
			base.RiskControl.BTCETHMaxLeverage = n
		}
		if n, ok := clampInt(rc, "altcoin_max_leverage", 1, 20, warnings, "山寨币杠杆上限"); ok {
			base.RiskControl.AltcoinMaxLeverage = n
		}
		if f, ok := clampFloat(rc, "btc_eth_max_position_value_ratio", 0.1, 20, warnings, "BTC/ETH 单仓价值比"); ok {
			base.RiskControl.BTCETHMaxPositionValueRatio = f
		}
		if f, ok := clampFloat(rc, "altcoin_max_position_value_ratio", 0.1, 20, warnings, "山寨币单仓价值比"); ok {
			base.RiskControl.AltcoinMaxPositionValueRatio = f
		}
		if f, ok := clampFloat(rc, "max_margin_usage", 0.1, 1.0, warnings, "保证金使用率"); ok {
			base.RiskControl.MaxMarginUsage = f
		}
		if f, ok := clampFloat(rc, "min_position_size", 1, 1000, warnings, "单仓最小金额"); ok {
			base.RiskControl.MinPositionSize = f
			if f < 12 {
				*warnings = append(*warnings, "单仓最小金额低于系统硬性下限 12 USDT，实盘仍会按 12 执行")
			}
		}
		if f, ok := clampFloat(rc, "min_risk_reward_ratio", 1, 10, warnings, "最小盈亏比"); ok {
			base.RiskControl.MinRiskRewardRatio = f
		}
		if n, ok := clampInt(rc, "min_confidence", 50, 99, warnings, "最低开仓信心分"); ok {
			base.RiskControl.MinConfidence = n
		}
	}
}

// validateCouncilConfig 终稿格式硬审核（返回错误清单，空 = 通过）
func validateCouncilConfig(cfg *store.StrategyConfig) []string {
	var errs []string
	if !validTradingModes[cfg.TradingMode] {
		errs = append(errs, "trading_mode 非法: "+cfg.TradingMode)
	}
	if !validSourceTypes[cfg.CoinSource.SourceType] {
		errs = append(errs, "coin_source.source_type 非法: "+cfg.CoinSource.SourceType)
	}
	if cfg.CoinSource.SourceType == "static" && len(cfg.CoinSource.StaticCoins) == 0 {
		errs = append(errs, "static 模式但固定币种列表为空")
	}
	if cfg.Indicators.Klines.PrimaryTimeframe == "" || !validTimeframes[cfg.Indicators.Klines.PrimaryTimeframe] {
		errs = append(errs, "主时间周期缺失或非法: "+cfg.Indicators.Klines.PrimaryTimeframe)
	}
	if cfg.Indicators.Klines.PrimaryCount < 10 || cfg.Indicators.Klines.PrimaryCount > 500 {
		errs = append(errs, fmt.Sprintf("主周期K线数量超出范围: %d", cfg.Indicators.Klines.PrimaryCount))
	}
	if cfg.RiskControl.MaxPositions < 1 || cfg.RiskControl.MaxPositions > 10 {
		errs = append(errs, fmt.Sprintf("最大持仓数超出范围: %d", cfg.RiskControl.MaxPositions))
	}
	if cfg.RiskControl.BTCETHMaxLeverage < 1 || cfg.RiskControl.BTCETHMaxLeverage > 20 {
		errs = append(errs, fmt.Sprintf("BTC/ETH 杠杆超出范围: %d", cfg.RiskControl.BTCETHMaxLeverage))
	}
	if cfg.RiskControl.AltcoinMaxLeverage < 1 || cfg.RiskControl.AltcoinMaxLeverage > 20 {
		errs = append(errs, fmt.Sprintf("山寨币杠杆超出范围: %d", cfg.RiskControl.AltcoinMaxLeverage))
	}
	if cfg.RiskControl.MaxMarginUsage <= 0 || cfg.RiskControl.MaxMarginUsage > 1 {
		errs = append(errs, fmt.Sprintf("保证金使用率非法: %.2f", cfg.RiskControl.MaxMarginUsage))
	}
	return errs
}

// ---------- 会诊执行引擎 ----------

// callCouncilAI 调用指定模型（复用 runRealAITest）
func (s *Server) callCouncilAI(userID, modelID, systemPrompt, userPrompt string) (string, error) {
	return s.runRealAITest(userID, modelID, systemPrompt, userPrompt)
}

// runCouncil 执行完整会诊流程（在后台 goroutine 中运行）
func (s *Server) runCouncil(st *councilState, modelID string, base *store.StrategyConfig) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[Council] panic: %v", r)
			st.setStatus("failed", fmt.Sprintf("会诊内部错误: %v", r))
		}
	}()

	lang := st.Language
	summaryLang := "中文"
	if lang == "en" {
		summaryLang = "English"
	}

	getStep := func(roleID string) *councilStep {
		for _, step := range st.Steps {
			if step.Role == roleID {
				return step
			}
		}
		return nil
	}

	// runOneRole 执行单个角色（step 字段写入持 st.mu 锁，保证与 GET handler 并发安全）
	runOneRole := func(roleID, userPrompt string) (*councilEnvelope, error) {
		role := councilRoleDef{}
		for _, r := range councilRoles {
			if r.ID == roleID {
				role = r
				break
			}
		}
		step := getStep(roleID)
		if step == nil {
			return nil, fmt.Errorf("unknown role %s", roleID)
		}
		if st.cancelFlag.Load() {
			st.mu.Lock()
			step.Status = string(councilStepSkipped)
			st.mu.Unlock()
			return nil, fmt.Errorf("cancelled")
		}
		st.mu.Lock()
		step.Status = string(councilStepRunning)
		st.mu.Unlock()
		start := time.Now()

		resp, err := s.callCouncilAI(st.UserID, modelID, councilTaskPrompt(role, lang), userPrompt)
		if err != nil {
			st.mu.Lock()
			step.Status = string(councilStepFailed)
			step.Error = err.Error()
			step.DurationMs = time.Since(start).Milliseconds()
			st.mu.Unlock()
			return nil, err
		}
		env, err := parseEnvelope(resp)
		if err != nil {
			st.mu.Lock()
			step.Status = string(councilStepFailed)
			step.Error = err.Error()
			step.DurationMs = time.Since(start).Milliseconds()
			st.mu.Unlock()
			return nil, err
		}
		st.mu.Lock()
		step.Status = string(councilStepDone)
		step.Summary = env.Summary
		step.Concerns = env.Concerns
		step.Payload = env.Payload
		step.DurationMs = time.Since(start).Milliseconds()
		st.mu.Unlock()
		return env, nil
	}

	// 汇总上游 memo 的辅助函数
	buildUpstreamContext := func(roleIDs ...string) string {
		var sb strings.Builder
		for _, rid := range roleIDs {
			step := getStep(rid)
			if step == nil || step.Status != string(councilStepDone) {
				continue
			}
			payloadJSON, _ := json.MarshalIndent(step.Payload, "", "  ")
			fmt.Fprintf(&sb, "### 来自 %s%s 的备忘录\n%s\n", step.Emoji, roleDisplayName(rid, lang), string(payloadJSON))
			if len(step.Concerns) > 0 {
				fmt.Fprintf(&sb, "该角色的疑虑: %s\n", strings.Join(step.Concerns, "；"))
			}
			sb.WriteString("\n")
		}
		return sb.String()
	}

	intentBlock := fmt.Sprintf("## 用户意图\n%s\n\n（输出语言: %s）", st.Intent, summaryLang)

	// ---------- 第 0 步：情报规划（仅当 SearXNG 已配置）----------
	sx := NewSearxngClient()
	marketBrief, coinBrief := "", ""
	if sx.Enabled() {
		if env, err := runOneRole("intel_planner", intentBlock+"\n请规划搜索关键词。"); err == nil && env != nil {
			marketQueries, _ := env.Payload["market_queries"].([]any)
			coinQueries, _ := env.Payload["coin_queries"].([]any)
			var sourceTitles []string
			collect := func(queries []any, langQ string) string {
				var all []SearxngResult
				for _, q := range queries {
					if qs, ok := q.(string); ok && strings.TrimSpace(qs) != "" {
						results, err := sx.Search(qs, langQ, 5)
						if err != nil {
							logger.Infof("[Council] SearXNG search failed (%s): %v", qs, err)
							continue
						}
						all = append(all, results...)
					}
				}
				// 旧新闻过滤：只保留 30 天内的结果；无日期结果保留
				cutoff := time.Now().AddDate(0, 0, -30)
				fresh := all[:0]
				for _, r := range all {
					if t, ok := parseCouncilDate(r.PublishedDate); ok && t.Before(cutoff) {
						continue
					}
					fresh = append(fresh, r)
				}
				all = fresh
				// 按发布时间降序（新的在前）
				sort.Slice(all, func(i, j int) bool {
					ti, oki := parseCouncilDate(all[i].PublishedDate)
					tj, okj := parseCouncilDate(all[j].PublishedDate)
					if oki != okj {
						return oki
					}
					return oki && ti.After(tj)
				})
				for _, r := range all {
					// 来源 chip 带上发布日期，方便识别新旧
					date := ""
					if t, ok := parseCouncilDate(r.PublishedDate); ok {
						date = t.Format("01-02") + " "
					}
					sourceTitles = append(sourceTitles, date+r.Title)
				}
				return BuildBrief(all)
			}
			marketBrief = collect(marketQueries, lang)
			coinBrief = collect(coinQueries, lang)
			if step := getStep("intel_planner"); step != nil {
				st.mu.Lock()
				step.Sources = sourceTitles
				if marketBrief == "" && coinBrief == "" {
					step.Summary = "联网搜索无结果，团队将基于模型知识判断"
				}
				st.mu.Unlock()
			}
		}
	} else {
		if step := getStep("intel_planner"); step != nil {
			st.mu.Lock()
			step.Status = string(councilStepSkipped)
			step.Summary = "未配置 SearXNG（SEARXNG_URL），跳过联网情报"
			st.mu.Unlock()
		}
	}
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	// ---------- 第 1 轮：情报收集（并行）----------
	intelSection := ""
	if marketBrief != "" {
		intelSection += "### 市场情报简报（SearXNG 联网检索）\n" + marketBrief + "\n"
	}
	if coinBrief != "" {
		intelSection += "### 币种情报简报（SearXNG 联网检索）\n" + coinBrief + "\n"
	}

	var wg sync.WaitGroup
	var errAnalyst, errResearcher error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errAnalyst = runOneRole("market_analyst", intentBlock+"\n"+intelSection+"请给出市场画像。")
	}()
	go func() {
		defer wg.Done()
		_, errResearcher = runOneRole("coin_researcher", intentBlock+"\n"+intelSection+"请给出币种画像。")
	}()
	wg.Wait()
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}
	// 单个情报角色失败不终止会诊，记录后继续
	if errAnalyst != nil {
		logger.Warnf("[Council] market_analyst failed: %v", errAnalyst)
	}
	if errResearcher != nil {
		logger.Warnf("[Council] coin_researcher failed: %v", errResearcher)
	}
	if errAnalyst != nil && errResearcher != nil {
		st.setStatus("failed", "情报收集阶段全部失败："+errAnalyst.Error()+"；"+errResearcher.Error())
		return
	}

	// ---------- 第 2 轮：策略制定（串行接力）----------
	upstream1 := buildUpstreamContext("market_analyst", "coin_researcher")
	_, err := runOneRole("chief_trader", intentBlock+"\n"+upstream1+"\n请给出实战交易计划。")
	if err != nil {
		st.setStatus("failed", "首席合约交易员失败: "+err.Error())
		return
	}
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	_, err = runOneRole("strategy_architect", intentBlock+"\n"+upstream1+buildUpstreamContext("chief_trader")+"\n请确定交易模式与整体思路。")
	if err != nil {
		st.setStatus("failed", "策略架构师失败: "+err.Error())
		return
	}
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	// 周期指标工程师 与 币种来源规划师 可并行（互不依赖）
	// 周期指标工程师额外获得多周期真实K线数据（周K/日K/4小时K，各自独立一节）
	upstreamR2 := buildUpstreamContext("market_analyst", "coin_researcher", "chief_trader", "strategy_architect")
	klineSection := buildMultiTimeframeKlineSection(extractCouncilSymbol(st.Intent))
	if klineSection != "" {
		klineSection = "## 多周期真实K线数据（逐周期独立分析，每个时间区间单独判断）\n" + klineSection
	}
	var errEngineer, errCoinPlanner error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errEngineer = runOneRole("timeframe_engineer", intentBlock+"\n"+upstreamR2+"\n"+klineSection+"\n请先对周K/日K/4小时K分别给出趋势判断，再给出周期与指标配置。")
	}()
	go func() {
		defer wg.Done()
		_, errCoinPlanner = runOneRole("coin_planner", intentBlock+"\n"+upstreamR2+"\n请给出币种来源规划。")
	}()
	wg.Wait()
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}
	if errEngineer != nil {
		st.setStatus("failed", "周期指标工程师失败: "+errEngineer.Error())
		return
	}
	if errCoinPlanner != nil {
		st.setStatus("failed", "币种来源规划师失败: "+errCoinPlanner.Error())
		return
	}

	// ---------- 第 3 轮：审核定稿 ----------
	_, err = runOneRole("risk_officer", intentBlock+"\n"+buildUpstreamContext(
		"market_analyst", "coin_researcher", "chief_trader", "strategy_architect", "timeframe_engineer", "coin_planner")+
		"\n请给出风控参数、扫描周期建议，并对策略层进行独立审查。")
	if err != nil {
		st.setStatus("failed", "风控官失败: "+err.Error())
		return
	}
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	// 首席评审（含程序审核修复环，最多 2 轮）
	modeNote := ""
	if st.Mode == "modify" {
		currentJSON, _ := json.MarshalIndent(base, "", "  ")
		modeNote = fmt.Sprintf("\n## 当前策略配置（修改基准，在其基础上优化，不要无故推翻仍合理的部分）\n%s\n", string(currentJSON))
	}

	reviewerUser := func(extra string) string {
		return intentBlock + modeNote + buildUpstreamContext(
			"market_analyst", "coin_researcher", "chief_trader", "strategy_architect",
			"timeframe_engineer", "coin_planner", "risk_officer") + extra + "\n请输出最终策略配置终稿。"
	}

	var clampWarnings []string
	var scanIntervalSuggestion int
	var reasoning string
	repairRounds := 0
	extra := ""

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			repairRounds++
		}
		envReviewer, err := runOneRole("chief_reviewer", reviewerUser(extra))
		if err != nil {
			st.setStatus("failed", "首席评审失败: "+err.Error())
			return
		}
		if st.cancelFlag.Load() {
			st.setStatus("cancelled", "")
			return
		}

		// 合并终稿到基准配置
		candidate := *base
		mergeCouncilConfig(&candidate, envReviewer.Payload, &clampWarnings)

		if si, ok := clampInt(envReviewer.Payload, "scan_interval_minutes", 1, 1440, &clampWarnings, "扫描周期建议"); ok {
			scanIntervalSuggestion = si
		}
		reasoning = envReviewer.Summary

		// 程序格式硬审核
		errs := validateCouncilConfig(&candidate)
		if len(errs) == 0 {
			*base = candidate
			break
		}
		if attempt == 2 {
			st.setStatus("failed", "终稿未通过程序格式审核: "+strings.Join(errs, "；"))
			return
		}
		// 带错误清单打回首席评审修复
		errList, _ := json.Marshal(errs)
		extra = fmt.Sprintf("\n## ⚠️ 上一次终稿未通过程序格式审核，请修复以下问题后重新输出完整终稿\n%s\n", string(errList))
	}

	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	// ---------- 第 4 轮：首席策略撰写官（生成 System Prompt 四段）----------
	// 参数已定稿，由撰写官基于全部团队结论撰写可实盘执行的提示词策略
	upstreamWriter := buildUpstreamContext("market_analyst", "coin_researcher", "chief_trader", "strategy_architect", "timeframe_engineer", "coin_planner", "risk_officer")
	reviewNote := ""
	if reasoning != "" {
		reviewNote = fmt.Sprintf("### 首席评审终审裁决（参数已定稿）\n%s\n\n", reasoning)
	}
	writerExtra := ""
	for attempt := 0; attempt < 3; attempt++ {
		if st.cancelFlag.Load() {
			st.setStatus("cancelled", "")
			return
		}
		envWriter, err := runOneRole("prompt_writer", intentBlock+upstreamWriter+reviewNote+writerExtra+"\n请撰写完整的 System Prompt 策略（四段结构）。")
		if err != nil {
			// 撰写失败不毁掉会诊：保留默认/现有提示词
			clampWarnings = append(clampWarnings, "提示词撰写失败，已保留原提示词: "+err.Error())
			break
		}
		secs, verrs := extractWriterSections(envWriter.Payload)
		if len(verrs) == 0 {
			base.PromptSections.RoleDefinition = secs["role_definition"]
			base.PromptSections.TradingFrequency = secs["trading_frequency"]
			base.PromptSections.EntryStandards = secs["entry_standards"]
			base.PromptSections.DecisionProcess = secs["decision_process"]
			if cp, _ := envWriter.Payload["custom_prompt"].(string); strings.TrimSpace(cp) != "" {
				base.CustomPrompt = strings.TrimSpace(cp)
			}
			break
		}
		if attempt == 2 {
			clampWarnings = append(clampWarnings, "提示词未通过程序校验，已保留原提示词: "+strings.Join(verrs, "；"))
			break
		}
		errList, _ := json.Marshal(verrs)
		writerExtra = fmt.Sprintf("\n## ⚠️ 上一次撰写未通过程序校验，请修复后重新输出完整四段\n%s\n", string(errList))
	}

	st.setResult(&councilFinalResult{
		Config:                 base,
		ScanIntervalSuggestion: scanIntervalSuggestion,
		Reasoning:              reasoning,
		ClampWarnings:          clampWarnings,
		RepairRounds:           repairRounds,
	})
	st.setStatus("completed", "")
}

// roleDisplayName 角色显示名
func roleDisplayName(roleID, lang string) string {
	for _, r := range councilRoles {
		if r.ID == roleID {
			if lang == "en" {
				return r.NameEN
			}
			return r.NameZH
		}
	}
	return roleID
}

// ---------- API Handlers ----------

// handleStartStrategyAICouncil 创建并启动一次会诊（异步执行）
func (s *Server) handleStartStrategyAICouncil(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req struct {
		Intent       string               `json:"intent" binding:"required"`
		ModelID      string               `json:"model_id" binding:"required"`
		Language     string               `json:"language"`
		Mode         string               `json:"mode"`        // generate|modify
		Config       *store.StrategyConfig `json:"config"`
		AgentBudget  int                  `json:"agent_budget"` // Agent 全场调用总预算（5~40，默认12）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Intent) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写策略意图描述"})
		return
	}
	if req.Language != "en" {
		req.Language = "zh"
	}
	if req.Mode != "modify" {
		req.Mode = "generate"
	}
	if req.Mode == "modify" && req.Config == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "修改模式需要提供当前策略配置"})
		return
	}

	// 校验模型可用性
	model, err := s.store.AIModel().Get(userID, req.ModelID)
	if err != nil || model == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "AI 模型不存在"})
		return
	}
	if !model.Enabled || model.APIKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("模型 %s 未启用或缺少 API Key", model.Name)})
		return
	}

	// 基准配置：修改模式用当前配置；生成模式用默认模板
	var base *store.StrategyConfig
	if req.Mode == "modify" {
		base = req.Config
	} else {
		defaultCfg := store.GetDefaultStrategyConfig(req.Language)
		base = &defaultCfg
	}

	id := uuid.New().String()
	if req.AgentBudget == 0 {
		req.AgentBudget = 12
	}
	budget := req.AgentBudget
	if budget < 5 {
		budget = 5
	}
	if budget > 40 {
		budget = 40
	}
	st := &councilState{
		ID:        id,
		UserID:    userID,
		Status:    "running",
		Mode:      req.Mode,
		Intent:    req.Intent,
		Language:  req.Language,
		SearchOn:  getSearxngURL() != "",
		Budget:    budget,
		Steps:     make([]*councilStep, 0, len(councilRoles)),
		Transcript: make([]councilTranscriptEntry, 0),
		CreatedAt: time.Now(),
	}
	for _, r := range councilRoles {
		st.Steps = append(st.Steps, &councilStep{
			Role:   r.ID,
			Emoji:  r.Emoji,
			Status: string(councilStepPending),
			Round:  r.Round,
		})
	}

	cleanupStaleCouncils()
	councilStatesMu.Lock()
	councilStates[id] = st
	councilStatesMu.Unlock()

	go s.runCouncilAgent(st, req.ModelID, base)

	c.JSON(http.StatusOK, gin.H{"council_id": id})
}

// handleGetStrategyAICouncil 查询会诊状态（前端轮询）
func (s *Server) handleGetStrategyAICouncil(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	id := c.Param("id")

	councilStatesMu.RLock()
	st, ok := councilStates[id]
	councilStatesMu.RUnlock()
	if !ok || st.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "会诊不存在"})
		return
	}
	// 加读锁保护 Steps/Status/Result 的并发序列化（runCouncil goroutine 正在写入）
	st.mu.RLock()
	defer st.mu.RUnlock()
	c.JSON(http.StatusOK, st)
}

// handleCancelStrategyAICouncil 取消会诊
func (s *Server) handleCancelStrategyAICouncil(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	id := c.Param("id")

	councilStatesMu.RLock()
	st, ok := councilStates[id]
	councilStatesMu.RUnlock()
	if !ok || st.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "会诊不存在"})
		return
	}
	st.cancelFlag.Store(true)
	c.JSON(http.StatusOK, gin.H{"message": "取消信号已发送"})
}

// cleanupStaleCouncils 清理超过 1 小时的会诊状态
func cleanupStaleCouncils() {
	councilStatesMu.Lock()
	defer councilStatesMu.Unlock()
	for id, st := range councilStates {
		if time.Since(st.CreatedAt) > time.Hour {
			delete(councilStates, id)
		}
	}
}
