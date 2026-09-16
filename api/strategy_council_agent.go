package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"
)

// ---------- 受控自主 Agent 圆桌 ----------
// 5 个角色串行圆桌，前序发言全量可见（Transcript 消息流）；
// 角色可自主调用工具（K线/新闻），风控评审官可点名追问；
// 全场 AI 调用受用户设定的总预算约束，每次发言注入剩余预算。

var reToolBlock = regexp.MustCompile("(?s)```(?:tool|json)?\\s*(\\{.*?\\})\\s*```")
var reAskBlock = regexp.MustCompile("(?s)```(?:ask|json)?\\s*(\\{.*?\\})\\s*```")

const councilToolProtocol = `
## 工具（可自主调用，数据由系统实时拉取）
需要数据时输出一个独立代码块（格式如下），系统执行后把结果回传给你继续分析：
注意：围栏必须是 tool（不是 json），参数直接放顶层（不要包 args）。
` + "```tool" + `
{"tool": "get_klines", "symbol": "ETHUSDT", "interval": "4h", "limit": 60}
` + "```" + `
- get_klines：K线查询。symbol（带 USDT 后缀）、interval（5m/15m/30m/1h/4h/1d/1w）、limit（10~200）。返回区间统计+指标快照+最近K线明细。
` + "```tool" + `
{"tool": "search_news", "query": "ETH ETF 最新消息"}
` + "```" + `
- search_news：联网搜索最新资讯（带发布日期，已过滤30天前旧闻）
` + "```tool" + `
{"tool": "list_coins"}
` + "```" + `
- list_coins：查询主流币种列表 + 24h成交额Top15 + 涨幅Top15热门币（选币依据，交易对完全由你们决定）
- 连续工具调用最多 4 次；结论必须有数据支撑，请主动查证。`

const councilAskProtocol = `
## 点名追问机制
如需某个角色澄清或补充论据，输出一个独立代码块（每轮最多追问 1 次）：
` + "```ask" + `
{"target": "chief_trader", "question": "你的止损逻辑在单边极端行情下如何自处？"}
` + "```" + `
合法 target: intel_analyst / chief_trader / strategy_architect / risk_reviewer（不能是自己）。系统会把问题转给对方作答，回答会进入圆桌记录回到你。`

// councilAgentSystemPrompt Agent 角色系统提示词
// nativeTools: 模型走原生 function calling 时，指引改为「直接调用工具」，文本协议块另行拼接
func councilAgentSystemPrompt(role councilRoleDef, lang string, capital float64, promptStyle string, nativeTools bool) string {
	summaryLang := "中文"
	if lang == "en" {
		summaryLang = "English"
	}
	isDataRole := role.ID == "intel_analyst" || role.ID == "chief_trader" || role.ID == "strategy_architect"
	var toolCallInstruction string
	switch {
	case isDataRole && nativeTools:
		toolCallInstruction = "- 需要数据时直接调用系统提供的工具（原生 function calling），系统会把执行结果回传给你继续分析，不要手写工具代码块。"
	case isDataRole:
		toolCallInstruction = "- 需要数据时先输出工具代码块（严格按下方工具协议格式）；系统执行后把结果回传给你继续分析。"
	default:
		toolCallInstruction = "- 你没有工具调用权限；对前序发言有数据疑问时，输出 ask 代码块点名追问（严格按下方追问协议格式）。"
	}
	capitalSection := "用户未单独填写本金规模；若策略意图中提到资金（如「只有 10U」「500U」），以该表述为准并严格执行下方可开仓校验。"
	if capital > 0 {
		capitalSection = fmt.Sprintf("**用户本金: %.2f USDT**（真实资金，规则必须严格执行）。", capital)
	}
	base := fmt.Sprintf(`你是 NOFX 量化交易系统「策略专家团」成员：%s%s（Agent 模式）。团队围绕用户策略意图圆桌协作，按顺序发言，所有前序发言在「圆桌发言记录」中全量可见。

## 资金可开仓校验（硬性规则，违反即方案作废）
%s
币安 USDT-M 合约最小名义价值：BTC ≥ 100 USDT，ETH 及多数主流币 ≥ 20 USDT，部分小币 ≥ 5 USDT（以交易所实际规则为准）。
可开仓名义价值 = 本金 × 杠杆。若本金在最高允许杠杆下仍达不到某币种的最小名义价值，该币种**根本开不起仓，绝对禁止推荐**（例如 10U 本金 × 10x = 100U 名义，够 ETH 但不够 BTC 的 100U 门槛附近时必须查证并保守处理）。
推荐币种前必须先做这笔乘法验算；小本金（<50U）优先考虑合约面值小、精度友好的中小市值币种，并明确写出每个币的实际可开仓名义与仓位拆分。

## 圆桌礼仪（必须遵守）
- 发言开头必须先点评前序专家：点名引用其观点（如「🎯 首席合约交易员认为…」），明确表态赞同或反驳并给出理由，不许无视前序发言自说自话。
- 发现前序结论与数据矛盾时必须当面指出，这是你的职责。

## 输出要求（严格遵守）
最终结论只输出一个 JSON 对象（信封格式），不要输出任何其他文字：
%s

- summary 和 concerns 用 %s。
- payload 字段名和枚举值必须严格使用下方给定的英文值。
- 对上游结论有不同意见必须写入 concerns，供风控评审官裁决。
%s
`, role.Emoji, roleDisplayName(role.ID, lang), capitalSection, envelopeSchemaHint, summaryLang, toolCallInstruction)

	switch role.ID {
	case "intel_analyst":
		return base + `
## 你的任务（情报分析师）
你是团队的情报枢纽：判断当前加密货币市场状态，并给出关注币种的消息面画像。

必须先用工具查证：至少 1 次 search_news（市场大盘 + 意图相关币种），必要时 get_klines 辅助判断大盘结构。

payload 字段：
- market_regime: "trending_up" | "trending_down" | "ranging" | "volatile" | "extreme"
- bias: "bullish" | "bearish" | "neutral"
- key_points: string[]，2~4 条关键判断依据（注明来自搜索还是K线）
- coin_profiles: object[]: {"symbol": "ETHUSDT", "sentiment": "bullish|bearish|neutral", "catalysts": "近期催化剂", "volatility": "high|medium|low", "notes": "一句话点评"}
- suggested_coins: string[]，建议关注的币种（带 USDT 后缀，最多 5 个）
- 联网搜索不可用时基于模型知识判断，并在 concerns 注明"无联网情报"。`
	case "chief_trader":
		return base + `
## 你的任务（首席合约交易员）
你是身经百战的加密货币合约交易员：穿越多轮牛熊，经历过 312、519 级别的极端行情，纪律第一、生存第一，对杠杆始终心存敬畏。你深知大部分时间应该等待而不是交易。

必须先用 get_klines 查看意图相关币种的真实K线（至少主周期+更高周期各一次）再下结论。

payload 字段：
- market_view: 一句话盘面观点（引用真实数据）
- direction_preference: "long" | "short" | "both" | "neutral"
- expected_holding: 预期持仓周期 "scalp"(分钟级) | "intraday"(数小时) | "swing"(1~3天) | "position"(数天以上)
- entry_logic: 什么条件下入场（具体信号）
- no_trade_conditions: string[]，什么情况下坚决不做单（至少 2 条）
- leverage_attitude: "conservative" | "moderate" | "aggressive"（实战视角，通常不超过 moderate）`
	case "strategy_architect":
		return base + `
## 你的任务（策略架构师）
把交易员的实战计划翻译成可执行的策略参数。必须对周K(1w)/日K(1d)/4小时K(4h) 分别独立给出趋势判断（不许混在一起笼统描述），并标注周期间的共振或矛盾。

合法时间周期（只能从中选择）: "1m", "3m", "5m", "15m", "30m", "1h", "4h", "1d"
持仓周期 → 参考K线组合: scalp→1m/3m/5m 主周期; intraday→5m/15m/1h; swing→15m/1h/4h; position→1h/4h/1d

payload 字段：
- multi_tf_views: object[]，1w/1d/4h 三个周期每个都必须有: {"timeframe": "1w|1d|4h", "trend": "up|down|range", "key_evidence": "关键证据（EMA排列/RSI/MACD/价格结构，1~2句）"}
- trading_mode: "balanced"|"aggressive"|"conservative"|"scalping"
- coin_source: {"source_type": "static"|"coinpool"|"oi_top"|"mixed", "static_coins": string[]（带USDT后缀，最多10个）, "coin_pool_limit": number(5~30), "oi_top_limit": number(5~50)}
  - 短线高频适合 coinpool/mixed，专注研究某几个币用 static
- klines: {"primary_timeframe": "1m|3m|5m|15m|30m|1h|4h|1d", "primary_count": number(10~500，通常30~100), "enable_multi_timeframe": boolean, "selected_timeframes": string[](2~4个), "longer_timeframe": string, "longer_count": number}
- indicators: {"enable_ema": bool, "ema_periods": number[](如[20,50]), "enable_rsi": bool, "rsi_periods": number[](如[7,14]), "enable_macd": bool, "macd_fast_period": number, "macd_slow_period": number, "enable_atr": bool, "atr_periods": number[](如[14]), "enable_boll": bool, "boll_periods": number[](如[20]), "enable_volume": bool, "enable_oi": bool, "enable_funding_rate": bool}
  - 短线必须有 EMA/RSI 类快指标；波段以上建议 MACD/BOLL；OI 与成交量建议常开
- rationale: 1~2 句理由（需引用 multi_tf_views 结论与交易员计划）
- 必须与交易员的 expected_holding 和 leverage_attitude 一致，冲突写入 concerns`
	case "risk_reviewer":
		return base + `
## 你的任务（风控评审官）
你是独立第三方风控官兼专家团主席：对前序所有角色独立审查、裁决分歧、输出最终策略配置终稿。

审查要点：
- 风控参数是否匹配交易风格（对照 no_trade_conditions 和 leverage_attitude：架构激进而交易员保守时必须处理）
- K线周期组合是否与多周期趋势判断自洽
- 币种来源是否符合策略意图
- 发现疑点可用 ask 代码块点名追问当事人

payload 字段：
- risk_review: {"verdict": "pass"|"warning"|"reject", "comments": string[], "objections": [{"target_role": "角色ID", "issue": "问题", "suggestion": "修改建议"}]}
- final_config: object（终稿，字段与枚举必须严格遵守）:
  - trading_mode: "balanced"|"aggressive"|"conservative"|"scalping"
  - coin_source: {"source_type": "static"|"coinpool"|"oi_top"|"mixed", "static_coins": string[], "coin_pool_limit": number, "oi_top_limit": number}
  - klines: {"primary_timeframe": "1m|3m|5m|15m|30m|1h|4h|1d", "primary_count": number, "enable_multi_timeframe": boolean, "selected_timeframes": string[], "longer_timeframe": string, "longer_count": number}
  - indicators: {"enable_ema": bool, "ema_periods": number[], "enable_rsi": bool, "rsi_periods": number[], "enable_macd": bool, "macd_fast_period": number, "macd_slow_period": number, "enable_atr": bool, "atr_periods": number[], "enable_boll": bool, "boll_periods": number[], "enable_volume": bool, "enable_oi": bool, "enable_funding_rate": bool}
  - risk_control: {"max_positions": n(1~10), "btc_eth_max_leverage": n(1~20，建议≤10), "altcoin_max_leverage": n(1~20，建议≤5), "btc_eth_max_position_value_ratio": f(0.1~20), "altcoin_max_position_value_ratio": f(0.1~20), "max_margin_usage": f(0.1~1.0，建议≤0.9), "min_position_size": f(建议12), "min_risk_reward_ratio": f(建议3), "min_confidence": n(50~99，建议75)}
- scan_interval_minutes: 扫描周期建议（分钟，1~1440）：scalp→1~3, intraday→5~15, swing→15~60, position→60~240
- reasoning: 终审总结（做了哪些裁决、为什么）
- 裁决原则：安全性优先于收益性；交易员的 no_trade_conditions 除非有强理由否则采纳`
	case "prompt_writer":
		return base + `
## 你的任务（首席策略撰写官）
你是专家团的首席笔杆子：基于全部圆桌发言（交易计划/参数配置/终审裁决），为交易系统撰写 System Prompt 策略。这份提示词将直接作为 AI 交易员的行为准则。
` + writerStyleInstruction(promptStyle) + `
通用要求：
- 严禁写死与参数配置冲突的系统硬约束（最大持仓数、杠杆上限、保证金率等由程序注入），提示词不要重复定义。
- custom_prompt（可选）：额外禁忌或特色纪律；没有则输出空字符串 ""。
- 全文用 {{LANG}} 书写，语气坚定、面向执行者。

payload 字段：
- strategy_name: string，为这套策略取一个简短有力的名字（4~12字，贴合策略风格与方法论，禁止叫「策略A」这类无意义名字；用户语言为英文时取英文名）
- strategy_description: string，一句话策略简介（30字内）
- prompt_sections: {"role_definition": string, "trading_frequency": string, "entry_standards": string, "decision_process": string}（四段均必填）
- custom_prompt: string（可为 ""）
- style_note: 一句话说明本策略的风格定位与目标行情`
	}
	return base
}

// writerStyleInstruction 按用户选择的提示词风格返回撰写要求
func writerStyleInstruction(style string) string {
	switch style {
	case "concise":
		return `
## 写作风格：简洁（用户指定，必须严格遵守）
老手实战风格——每段只写 1~2 句话，四段合计不超过 80 字：
- role_definition：一句话人设身份（如「你是一个专业的加密货币优质资产配置员」）
- trading_frequency：一句话频率理念（如「不要频繁交易，做资产配置」）
- entry_standards：一句话核心标准（如「认真评估后决策，没把握就不开仓」）
- decision_process：一句话流程（如「输出结构化JSON，严格执行止损」）
抓核心人设与理念即可，不列条件组合、不写执行数字、不堆约束。`
	case "balanced":
		return `
## 写作风格：均衡（用户指定）
四段结构齐全，每段 2~4 句话：
- role_definition：人设身份 + 核心方法论（一句话点到即可）
- trading_frequency：评估频率 + 空仓条件 + 持仓周期预期
- entry_standards：2~3 条核心入场信号 + 最关键的禁入情形
- decision_process：简明决策步骤（查持仓→看候选→出决策）
保留关键执行纪律，但不逐条罗列完整禁忌清单与全部执行数字。`
	case "detailed":
		return `
## 写作风格：详细（用户指定）
风格对标真实实战策略提示词，质量标准是「拿来即可实盘」：
1. 有清晰的人设身份与方法论体系（如维科夫+SMC+缠论融合、马丁网格、动量突破等，依据策略意图选定）、具体的入场信号条件、明确的执行规则。
2. 必须具体可执行：写明明确的交易执行数字——信心分阈值、止损止盈距离或结构位规则、分批加减仓规则、持仓时长预期、移动止损规则等。拒绝空泛套话（如"注意风险""谨慎交易"这类没有操作含义的句子）。
3. 四段结构：
   - role_definition（角色定义）：人设身份 + 方法论体系 + 核心目标。核心目标必须包含：最大化夏普比率（平均回报/回报波动），纪律优先于利润。
   - trading_frequency（交易频率理念）：多久评估交易一次、什么情况必须空仓等待、持仓周期预期（按策略类型给具体时长范围）。
   - entry_standards（入场标准）：具体的信号组合条件（形态/指标/资金流/结构位满足什么才允许进场）、分批建仓规则、明确列出禁止入场的情形。
   - decision_process（决策流程）：从「检查现有持仓（止盈止损判断）→ 分析候选币种 → 输出决策」的完整步骤，含每步的执行纪律与思维链要求。`
	default: // auto
		return `
## 写作风格：自动（由你判断）
根据策略类型与圆桌讨论的深度，自行决定提示词的复杂度：
- 简单风格（配置型/趋势持有/纯理念型）：每段 1~2 句话，抓核心人设与理念即可，不堆约束。
- 均衡风格（常规波段/日内）：每段 2~4 句话，保留关键入场信号与执行纪律。
- 详细风格（马丁网格/多信号共振/需要严格纪律的复杂体系）：完整的四段结构，含具体执行数字与规则，拒绝空泛套话。
在 style_note 里用一句话说明你选择的复杂度和理由。`
	}
}

// addTranscript 追加圆桌发言记录（并发安全）
func (st *councilState) addTranscript(role, emoji, name, kind, content string, payload any) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Transcript = append(st.Transcript, councilTranscriptEntry{
		Role: role, Emoji: emoji, Name: name, Kind: kind,
		Content: content, Payload: payload, CreatedAt: time.Now(),
	})
}

// addActivity 追加步骤动作流水（并发安全）
func (st *councilState) addActivity(roleID, line string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, s := range st.Steps {
		if s.Role == roleID {
			s.Activity = append(s.Activity, line)
			break
		}
	}
}

// getStepLocked 按角色 ID 找步骤（调用方持有锁或知悉并发安全）
func getStepLocked(st *councilState, roleID string) *councilStep {
	for _, s := range st.Steps {
		if s.Role == roleID {
			return s
		}
	}
	return nil
}

// buildTranscriptText 把圆桌记录拼为提示词文本（含 payload 细节，防超长截断）
func buildTranscriptText(st *councilState) string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	var sb strings.Builder
	for _, e := range st.Transcript {
		switch e.Kind {
		case "speech":
			fmt.Fprintf(&sb, "【%s %s 发言】\n%s\n", e.Emoji, e.Name, e.Content)
			if e.Payload != nil {
				if pj, err := json.Marshal(e.Payload); err == nil {
					fmt.Fprintf(&sb, "payload: %s\n", truncateRunes(string(pj), 1800))
				}
			}
			sb.WriteString("\n")
		case "tool_call":
			fmt.Fprintf(&sb, "【%s %s 调用工具】%s\n", e.Emoji, e.Name, e.Content)
		case "tool_result":
			fmt.Fprintf(&sb, "【工具结果】\n%s\n\n", e.Content)
		case "question":
			fmt.Fprintf(&sb, "%s\n", e.Content)
		case "answer":
			fmt.Fprintf(&sb, "【%s %s 回应】\n%s\n", e.Emoji, e.Name, e.Content)
			if e.Payload != nil {
				if pj, err := json.Marshal(e.Payload); err == nil {
					fmt.Fprintf(&sb, "payload: %s\n", truncateRunes(string(pj), 1200))
				}
			}
			sb.WriteString("\n")
		default:
			fmt.Fprintf(&sb, "【%s %s】%s\n\n", e.Emoji, e.Name, e.Content)
		}
	}
	return sb.String()
}

// execCouncilTool 执行 Agent 工具调用，返回喂回 prompt 的结果文本
func (s *Server) execCouncilTool(call map[string]any, lang string) string {
	name, _ := call["tool"].(string)
	switch name {
	case "get_klines":
		symbol, _ := call["symbol"].(string)
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol == "" {
			symbol = "BTCUSDT"
		}
		if !coinSymbolPattern.MatchString(symbol) {
			return "错误: 无效币种 " + symbol
		}
		interval, _ := call["interval"].(string)
		validTf := map[string]bool{"5m": true, "15m": true, "30m": true, "1h": true, "4h": true, "1d": true, "1w": true}
		if !validTf[interval] {
			return "错误: 不支持的周期 " + interval + "（可用: 5m/15m/30m/1h/4h/1d/1w）"
		}
		limit := 60
		if v, ok := call["limit"].(float64); ok && v >= 10 && v <= 200 {
			limit = int(v)
		}
		client := market.NewAPIClient()
		klines, err := client.GetKlines(symbol, interval, limit)
		if err != nil || len(klines) < 5 {
			return "错误: K线获取失败"
		}
		n := len(klines)
		last := klines[n-1]
		high, low := klines[0].High, klines[0].Low
		for _, k := range klines {
			if k.High > high {
				high = k.High
			}
			if k.Low < low {
				low = k.Low
			}
		}
		chg := (last.Close - klines[0].Close) / klines[0].Close * 100
		closes := make([]float64, n)
		for i, k := range klines {
			closes[i] = k.Close
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%s %s K线 %d 根 | 区间涨跌 %+.2f%% | 高 %s 低 %s | EMA20 %s EMA50 %s | RSI14 %.1f | MACD柱 %s",
			symbol, interval, n, chg,
			formatCouncilPrice(high), formatCouncilPrice(low),
			formatCouncilPrice(councilEMA(closes, 20)), formatCouncilPrice(councilEMA(closes, 50)),
			councilRSI(closes, 14), formatCouncilPrice(func() float64 { _, _, h := councilMACD(closes); return h }()))
		sb.WriteString("\n最近12根（时间UTC 开/高/低/收/量）:")
		start := n - 12
		if start < 0 {
			start = 0
		}
		for i := start; i < n; i++ {
			k := klines[i]
			fmt.Fprintf(&sb, "\n%s O%s H%s L%s C%s V%.1f",
				time.UnixMilli(k.OpenTime).UTC().Format("01-02 15:04"),
				formatCouncilPrice(k.Open), formatCouncilPrice(k.High),
				formatCouncilPrice(k.Low), formatCouncilPrice(k.Close), k.Volume)
		}
		return sb.String()
	case "search_news":
		query, _ := call["query"].(string)
		query = strings.TrimSpace(query)
		if query == "" {
			return "错误: 搜索词为空"
		}
		sx := NewSearxngClient()
		if !sx.Enabled() {
			return "错误: 联网搜索未配置（SEARXNG_URL 未设置）"
		}
		results, err := sx.Search(query, lang, 6)
		if err != nil {
			return "错误: 搜索失败 " + err.Error()
		}
		if len(results) == 0 {
			return "搜索无结果，请换关键词"
		}
		return BuildBrief(results)
	case "list_coins":
		return s.execListCoins()
	default:
		return "错误: 未知工具 " + name
	}
}

// execListCoins 返回主流币种与近期热门币种（币安 USDT 永续 24hr ticker）
func (s *Server) execListCoins() string {
	httpClient := &http.Client{Timeout: 20 * time.Second}
	resp, err := httpClient.Get("https://fapi.binance.com/fapi/v1/ticker/24hr")
	if err != nil {
		return "错误: 币种行情获取失败 " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "错误: 读取行情响应失败"
	}
	type tick struct {
		Symbol             string  `json:"symbol"`
		LastPrice          float64 `json:"lastPrice,string"`
		PriceChangePercent float64 `json:"priceChangePercent,string"`
		QuoteVolume        float64 `json:"quoteVolume,string"`
	}
	var ticks []tick
	if err := json.Unmarshal(body, &ticks); err != nil {
		return "错误: 解析行情数据失败"
	}
	majors := map[string]bool{
		"BTCUSDT": true, "ETHUSDT": true, "SOLUSDT": true, "BNBUSDT": true,
		"XRPUSDT": true, "DOGEUSDT": true, "ADAUSDT": true, "AVAXUSDT": true,
		"LINKUSDT": true, "SUIUSDT": true,
	}
	// 过滤 USDT 永续、剔除杠杆代币等
	pool := make([]tick, 0, len(ticks))
	for _, t := range ticks {
		if strings.HasSuffix(t.Symbol, "USDT") && !strings.Contains(t.Symbol, "_") && t.QuoteVolume > 0 {
			pool = append(pool, t)
		}
	}
	byVol := append([]tick(nil), pool...)
	sort.Slice(byVol, func(i, j int) bool { return byVol[i].QuoteVolume > byVol[j].QuoteVolume })
	byChg := append([]tick(nil), pool...)
	sort.Slice(byChg, func(i, j int) bool { return byChg[i].PriceChangePercent > byChg[j].PriceChangePercent })

	var sb strings.Builder
	sb.WriteString("== 主流币（现货价/24h涨跌/24h额）==")
	for _, t := range ticks {
		if majors[t.Symbol] {
			fmt.Fprintf(&sb, "\n%s %s %+.2f%% %.0f万", t.Symbol, formatCouncilPrice(t.LastPrice), t.PriceChangePercent, t.QuoteVolume/1e4)
		}
	}
	sb.WriteString("\n== 成交额Top15（活跃度参考）==")
	for i := 0; i < len(byVol) && i < 15; i++ {
		t := byVol[i]
		fmt.Fprintf(&sb, "\n%s %s %+.2f%%", t.Symbol, formatCouncilPrice(t.LastPrice), t.PriceChangePercent)
	}
	sb.WriteString("\n== 24h涨幅Top15（近期热门）==")
	for i := 0; i < len(byChg) && i < 15; i++ {
		t := byChg[i]
		fmt.Fprintf(&sb, "\n%s %s %+.2f%%", t.Symbol, formatCouncilPrice(t.LastPrice), t.PriceChangePercent)
	}
	return sb.String()
}

// parseToolCalls 提取响应中的工具调用块（兼容 tool/json/裸围栏；自动展开 args 包装）
func parseToolCalls(resp string) []map[string]any {
	matches := reToolBlock.FindAllStringSubmatch(resp, -1)
	var calls []map[string]any
	for _, m := range matches {
		var c map[string]any
		if err := json.Unmarshal([]byte(m[1]), &c); err == nil {
			if _, ok := c["tool"].(string); ok {
				// 兼容部分模型把参数包在 args 里：展开到顶层
				if args, ok := c["args"].(map[string]any); ok {
					for k, v := range args {
						if _, exists := c[k]; !exists {
							c[k] = v
						}
					}
				}
				calls = append(calls, c)
			}
		}
	}
	return calls
}

// remainingBudget 剩余调用预算
func remainingBudget(st *councilState) int {
	return st.Budget - int(atomic.LoadInt32(&st.UsedBudget))
}

// councilNativeToolsDefs 专家团三个数据工具的原生 function calling 定义
func councilNativeToolsDefs() []mcp.Tool {
	return []mcp.Tool{
		{
			Type: "function",
			Function: mcp.FunctionDef{
				Name:        "get_klines",
				Description: "K线查询。返回区间统计+指标快照（EMA/MACD/RSI/ATR/BOLL）+最近K线明细",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"symbol":   map[string]any{"type": "string", "description": "币种交易对，带 USDT 后缀，如 ETHUSDT"},
						"interval": map[string]any{"type": "string", "description": "K线周期：5m/15m/30m/1h/4h/1d/1w"},
						"limit":    map[string]any{"type": "integer", "description": "K线数量，10~200"},
					},
					"required": []string{"symbol", "interval"},
				},
			},
		},
		{
			Type: "function",
			Function: mcp.FunctionDef{
				Name:        "search_news",
				Description: "联网搜索加密货币最新资讯（带发布日期，已过滤30天前旧闻）",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "搜索关键词，如「ETH ETF 最新消息」"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: mcp.FunctionDef{
				Name:        "list_coins",
				Description: "查询主流币种列表 + 24h成交额Top15 + 涨幅Top15热门币（选币依据，交易对完全由团队决定）",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
}

// councilNativeToolCalling 判断该模型是否已通过能力检测（支持原生工具调用）
func (s *Server) councilNativeToolCalling(userID, modelID string) bool {
	model, err := s.store.AIModel().Get(userID, modelID)
	if err != nil || model == nil || model.Capabilities == "" {
		return false
	}
	var caps map[string]bool
	if json.Unmarshal([]byte(model.Capabilities), &caps) != nil {
		return false
	}
	return caps["tool_call"] == true
}

// callCouncilAINative 原生 function calling 调用（请求携带 Tools 定义，
// 模型返回的 tool_calls 由 mcp 层桥接为文本协议代码块，上层解析零改动）
func (s *Server) callCouncilAINative(userID, modelID, systemPrompt, userPrompt string) (string, error) {
	model, err := s.store.AIModel().Get(userID, modelID)
	if err != nil {
		return "", fmt.Errorf("failed to get AI model: %w", err)
	}
	if !model.Enabled {
		return "", fmt.Errorf("AI model %s is not enabled", model.Name)
	}
	if model.APIKey == "" {
		return "", fmt.Errorf("AI model %s is missing API Key", model.Name)
	}
	client := newAIClientForModel(model)
	req := &mcp.Request{
		Messages: []mcp.Message{
			mcp.NewSystemMessage(systemPrompt),
			mcp.NewUserMessage(userPrompt),
		},
		Tools:      councilNativeToolsDefs(),
		ToolChoice: "auto",
	}
	return client.CallWithRequest(req)
}

// runAgentTurn 执行一个角色的一轮 Agent 发言（含工具循环/追问循环），返回最终信封
// instruction: 本次发言的任务指令
func (s *Server) runAgentTurn(st *councilState, modelID string, roleID, instruction string) (*councilEnvelope, error) {
	var role councilRoleDef
	for _, r := range councilRoles {
		if r.ID == roleID {
			role = r
			break
		}
	}
	step := getStepLocked(st, roleID)
	if step == nil {
		return nil, fmt.Errorf("unknown role %s", roleID)
	}
	toolUses := 0
	askUsed := false
	// 模型已通过能力检测（tool_call）时走原生 function calling，否则回退文本协议
	nativeTools := s.councilNativeToolCalling(st.UserID, modelID)
	// 数据型角色才有工具权限：情报分析师/交易员/架构师
	canTool := roleID == "intel_analyst" || roleID == "chief_trader" || roleID == "strategy_architect"

	for turn := 0; turn < 10; turn++ {
		if st.cancelFlag.Load() {
			st.mu.Lock()
			step.Status = string(councilStepSkipped)
			st.mu.Unlock()
			return nil, fmt.Errorf("cancelled")
		}
		remain := remainingBudget(st)
		if remain <= 0 {
			st.mu.Lock()
			step.Status = string(councilStepSkipped)
			step.Summary = "调用预算耗尽"
			st.mu.Unlock()
			return nil, fmt.Errorf("budget exhausted")
		}

		// 组装本轮 prompt：意图 + 圆桌记录 + 指令 + 预算提示
		var sb strings.Builder
		fmt.Fprintf(&sb, "## 用户意图\n%s\n\n## 圆桌发言记录（前序全部发言）\n%s\n## 你的发言任务\n%s\n", st.Intent, buildTranscriptText(st), instruction)
		if remain <= 2 {
			sb.WriteString(fmt.Sprintf("\n【系统】剩余调用预算仅 %d 次：禁止调用工具与追问，立即输出最终信封 JSON。\n", remain))
		} else {
			sb.WriteString(fmt.Sprintf("\n【系统】全场剩余调用预算 %d 次（含工具调用后的继续发言）。\n", remain))
		}

		atomic.AddInt32(&st.UsedBudget, 1)
		sysP := councilAgentSystemPrompt(role, st.Language, st.Capital, st.PromptStyle, nativeTools)
		if !nativeTools && canTool {
			// 文本协议回退模式：必须给出工具调用格式说明，否则模型不知道怎么发起工具调用
			sysP += councilToolProtocol
		}
		// 追问机制不是原生工具，两种模式下都用文本协议
		sysP += councilAskProtocol
		var resp string
		var err error
		if nativeTools {
			// 原生 function calling：请求携带 Tools 定义，模型直接发起 tool_calls（mcp 层桥接回文本协议块）
			resp, err = s.callCouncilAINative(st.UserID, modelID, sysP, sb.String())
		} else {
			// 回退：文本协议（AI 自主输出 ```tool 代码块）
			resp, err = s.callCouncilAI(st.UserID, modelID, sysP, sb.String())
		}
		if err != nil {
			st.mu.Lock()
			step.Status = string(councilStepFailed)
			step.Error = err.Error()
			st.mu.Unlock()
			return nil, err
		}

		// 1) 工具调用（仅数据型角色：情报分析师/交易员/架构师）
		if calls := parseToolCalls(resp); len(calls) > 0 && canTool && toolUses < 4 && remain > 2 {
			for _, c := range calls {
				toolName, _ := c["tool"].(string)
				desc := toolName
				if toolName == "get_klines" {
					desc = fmt.Sprintf("get_klines %v %v×%v", c["symbol"], c["interval"], c["limit"])
				} else if toolName == "search_news" {
					desc = fmt.Sprintf("search_news %v", c["query"])
				}
				st.addTranscript(role.ID, role.Emoji, roleDisplayName(role.ID, st.Language), "tool_call", desc, nil)
				st.addActivity(role.ID, "🔧 "+desc)
				result := s.execCouncilTool(c, st.Language)
				st.addTranscript("system", "📊", "System", "tool_result", truncateRunes(result, 3000), nil)
			}
			toolUses += len(calls)
			continue
		}

		// 2) 点名追问（所有角色均可，每轮最多 1 次，需预算足够）——专家之间互相提问回答
		if m := reAskBlock.FindStringSubmatch(resp); m != nil && !askUsed && remainingBudget(st) >= 2 {
			var ask struct {
				Target   string `json:"target"`
				Question string `json:"question"`
			}
			if err := json.Unmarshal([]byte(m[1]), &ask); err == nil && ask.Question != "" {
				target := ask.Target
				var tRole councilRoleDef
				for _, r := range councilRoles {
					if r.ID == target {
						tRole = r
						break
					}
				}
				if tRole.ID != "" && tRole.ID != roleID {
					askUsed = true
					askerName := roleDisplayName(roleID, st.Language)
					st.addTranscript(roleID, role.Emoji, askerName, "question",
						fmt.Sprintf("【%s %s → %s %s 提问】%s", role.Emoji, askerName, tRole.Emoji, roleDisplayName(tRole.ID, st.Language), ask.Question), nil)
					st.addActivity(roleID, "❓ 向 "+roleDisplayName(tRole.ID, st.Language)+" 提问: "+truncateRunes(ask.Question, 60))
					// 被点名角色作答一轮（一次性，信封格式；作答允许 payload 为 {}，summary 为主）
					if remainingBudget(st) > 0 {
						atomic.AddInt32(&st.UsedBudget, 1)
						ansPrompt := fmt.Sprintf("## 用户意图\n%s\n\n## 圆桌发言记录\n%s\n## 提问\n%s %s 向你提问：%s\n\n请直接作答：输出信封 JSON，summary 为你的圆桌回应内容，payload 填空对象 {} 即可。", st.Intent, buildTranscriptText(st), role.Emoji, askerName, ask.Question)
						resp2, err2 := s.callCouncilAI(st.UserID, modelID, councilAgentSystemPrompt(tRole, st.Language, st.Capital, st.PromptStyle, false), ansPrompt)
						if err2 == nil {
							env2, errP := parseEnvelope(resp2)
							if errP != nil {
								// 宽容兜底：作答只关心 summary 内容，不因格式问题丢弃回答
								if block, ok := extractJSONBlock(resp2); ok {
									var raw map[string]any
									if json.Unmarshal([]byte(block), &raw) == nil {
										if sm, okS := raw["summary"].(string); okS && strings.TrimSpace(sm) != "" {
											env2 = &councilEnvelope{Summary: sm}
											errP = nil
										}
									}
								}
								if errP != nil {
									env2 = &councilEnvelope{Summary: truncateRunes(strings.TrimSpace(resp2), 600)}
									errP = nil
								}
							}
							content := env2.Summary
							if len(env2.Concerns) > 0 {
								content += "\n（疑虑: " + strings.Join(env2.Concerns, "；") + "）"
							}
							st.addTranscript(tRole.ID, tRole.Emoji, roleDisplayName(tRole.ID, st.Language), "answer", content, env2.Payload)
							st.addActivity(tRole.ID, "💬 回应追问")
						} else {
							logger.Warnf("[Council] answer from %s failed: %v", target, err2)
						}
					}
					continue
				}
			}
		}

		// 3) 信封解析
		env, errP := parseEnvelope(resp)
		if errP != nil {
			// 降级兜底：非关键角色（非终稿/非撰写）的直接把文字结论当作 summary，避免格式问题反复烧预算
			// 关键角色（风控评审官/撰写官）必须有结构化 payload，仍走重试
			cleanResp := strings.TrimSpace(reToolBlock.ReplaceAllString(resp, ""))
			cleanResp = strings.TrimSpace(reAskBlock.ReplaceAllString(cleanResp, ""))
			if roleID != "risk_reviewer" && roleID != "prompt_writer" && len([]rune(cleanResp)) > 30 {
				env = &councilEnvelope{
					Summary:  truncateRunes(cleanResp, 1500),
					Payload:  map[string]any{},
					Concerns: []string{},
				}
				errP = nil
			}
		}
		if errP != nil {
			// 格式错误：给一次重试机会（提示带原因，帮 AI 自纠）
			if turn < 8 {
				retryMsg := "输出格式错误（" + errP.Error() + "）"
				if len(parseToolCalls(resp)) > 0 && (!canTool || toolUses >= 4 || remain <= 2) {
					retryMsg += "：你输出的工具代码块不会被执行（次数已用尽或无工具权限），不要再输出工具块"
				}
				retryMsg += "，请立即只输出最终信封 JSON（以 { 开头 } 结尾），不要输出任何其他文字或代码块。"
				st.addTranscript("system", "⚠️", "System", "system", retryMsg, nil)
				continue
			}
			st.mu.Lock()
			step.Status = string(councilStepFailed)
			step.Error = errP.Error()
			st.mu.Unlock()
			return nil, errP
		}

		// 成功：写 transcript + step
		content := env.Summary
		if len(env.Concerns) > 0 {
			content += "\n（疑虑: " + strings.Join(env.Concerns, "；") + "）"
		}
		st.addTranscript(roleID, role.Emoji, roleDisplayName(roleID, st.Language), "speech", content, env.Payload)
		st.addActivity(role.ID, "✅ 已给出结论")
		st.mu.Lock()
		step.Status = string(councilStepDone)
		step.Summary = env.Summary
		step.Concerns = env.Concerns
		step.Payload = env.Payload
		st.mu.Unlock()
		return env, nil
	}
	st.mu.Lock()
	step.Status = string(councilStepFailed)
	step.Error = "超出轮次上限"
	st.mu.Unlock()
	return nil, fmt.Errorf("exceeded turn limit")
}

// runCouncilAgent 受控自主 Agent 圆桌主流程（后台 goroutine）
func (s *Server) runCouncilAgent(st *councilState, modelID string, base *store.StrategyConfig) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[Council] panic: %v", r)
			st.setStatus("failed", fmt.Sprintf("会诊内部错误: %v", r))
		}
	}()

	var clampWarnings []string
	var reasoning string
	scanIntervalSuggestion := 0
	repairRounds := 0

	// 圆桌开场
	opening := "策略意图: " + st.Intent
	if st.Capital > 0 {
		opening += fmt.Sprintf("\n用户本金: %.2f USDT（所有币种推荐必须通过可开仓校验：本金×杠杆 ≥ 该币最小名义价值，BTC≥100U，主流币≥20U）", st.Capital)
	}
	if s.councilNativeToolCalling(st.UserID, modelID) {
		opening += "\n工具调用模式: 原生 function calling（模型能力检测通过）"
	} else {
		opening += "\n工具调用模式: 文本协议（模型未通过原生工具检测，自动回退）"
	}
	st.addTranscript("system", "🏛️", "System", "system", opening, nil)

	// ---------- 第 1 轮：情报分析师 ----------
	if _, err := s.runAgentTurn(st, modelID, "intel_analyst", "请给出市场状态与币种画像（先用工具查证再下结论）。"); err != nil {
		if err.Error() == "cancelled" {
			st.setStatus("cancelled", "")
			return
		}
		if err.Error() != "budget exhausted" {
			st.setStatus("failed", "情报分析师失败: "+err.Error())
			return
		}
	}
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	// ---------- 第 1 轮：首席合约交易员 ----------
	if _, err := s.runAgentTurn(st, modelID, "chief_trader", "请先点名点评情报分析师的结论（赞同或反驳+理由），再基于圆桌记录与真实K线给出实战交易计划。推荐的每个币种必须通过可开仓校验（本金×杠杆≥最小名义价值，开不起仓的币直接排除）。"); err != nil {
		if err.Error() == "cancelled" {
			st.setStatus("cancelled", "")
			return
		}
		if err.Error() != "budget exhausted" {
			st.setStatus("failed", "首席合约交易员失败: "+err.Error())
			return
		}
	}
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	// ---------- 第 2 轮：策略架构师 ----------
	if _, err := s.runAgentTurn(st, modelID, "strategy_architect", "请先点名点评交易员的计划（哪些采纳哪些有保留），再给出完整参数配置。交易对完全由你们决定：默认模板的 BTC/ETH 只是起点，可用 list_coins 查询主流与热门币后自由增删 static_coins（须通过可开仓校验）；并含 1w/1d/4h 逐周期趋势判断、K线与指标。"); err != nil {
		if err.Error() == "cancelled" {
			st.setStatus("cancelled", "")
			return
		}
		if err.Error() != "budget exhausted" {
			st.setStatus("failed", "策略架构师失败: "+err.Error())
			return
		}
	}
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	// ---------- 第 2 轮：风控评审官（可追问 + 终稿修复循环）----------
	configMerged := false
	extra := ""
	for attempt := 0; attempt < 3; attempt++ {
		if st.cancelFlag.Load() {
			st.setStatus("cancelled", "")
			return
		}
		instr := "请审查前序全部发言：对每位专家点名给出裁决（赞同/否决+理由），发现疑点先用 ask 代码块点名追问一位专家再终审，最后给出风控意见与最终策略配置终稿（final_config）。"
		if extra != "" {
			instr += extra
		}
		envReviewer, err := s.runAgentTurn(st, modelID, "risk_reviewer", instr)
		if err != nil {
			if err.Error() == "cancelled" {
				st.setStatus("cancelled", "")
				return
			}
			if err.Error() == "budget exhausted" {
				break
			}
			st.setStatus("failed", "风控评审官失败: "+err.Error())
			return
		}
		candidate := *base
		mergeCouncilConfig(&candidate, envReviewer.Payload, &clampWarnings)
		if si, ok := clampInt(envReviewer.Payload, "scan_interval_minutes", 1, 1440, &clampWarnings, "扫描周期建议"); ok {
			scanIntervalSuggestion = si
		}
		reasoning = envReviewer.Summary
		errs := validateCouncilConfig(&candidate)
		if len(errs) == 0 {
			*base = candidate
			configMerged = true
			break
		}
		if attempt == 2 {
			st.setStatus("failed", "终稿未通过程序格式审核: "+strings.Join(errs, "；"))
			return
		}
		repairRounds++
		errList, _ := json.Marshal(errs)
		extra = "\n⚠️ 上一次终稿未通过程序格式审核，请修复以下问题后重新输出完整终稿:\n" + string(errList)
	}
	if st.cancelFlag.Load() {
		st.setStatus("cancelled", "")
		return
	}

	// ---------- 第 3 轮：首席策略撰写官 ----------
	promptWritten := false
	strategyName, strategyDesc := "", ""
	writerExtra := ""
	for attempt := 0; attempt < 3; attempt++ {
		if st.cancelFlag.Load() {
			st.setStatus("cancelled", "")
			return
		}
		if remainingBudget(st) <= 0 {
			break
		}
		styleName := map[string]string{"concise": "简洁", "balanced": "均衡", "detailed": "详细", "auto": "自动（由你判断复杂度）"}[st.PromptStyle]
		instr := "请先点名引用交易员与架构师的核心观点（赞同什么、规避什么），再基于全部圆桌发言撰写完整的 System Prompt 策略（四段结构），并为策略取名。写作风格必须采用「" + styleName + "」。"
		if reasoning != "" {
			instr += "\n终审裁决参考: " + truncateRunes(reasoning, 600)
		}
		instr += writerExtra
		envWriter, err := s.runAgentTurn(st, modelID, "prompt_writer", instr)
		if err != nil {
			if err.Error() == "cancelled" {
				st.setStatus("cancelled", "")
				return
			}
			clampWarnings = append(clampWarnings, "提示词撰写失败，已保留原提示词: "+err.Error())
			break
		}
		secs, verrs := extractWriterSections(envWriter.Payload, st.PromptStyle)
		if len(verrs) == 0 {
			base.PromptSections.RoleDefinition = secs["role_definition"]
			base.PromptSections.TradingFrequency = secs["trading_frequency"]
			base.PromptSections.EntryStandards = secs["entry_standards"]
			base.PromptSections.DecisionProcess = secs["decision_process"]
			if cp, _ := envWriter.Payload["custom_prompt"].(string); strings.TrimSpace(cp) != "" {
				base.CustomPrompt = strings.TrimSpace(cp)
			}
			if n, _ := envWriter.Payload["strategy_name"].(string); strings.TrimSpace(n) != "" {
				strategyName = truncateRunes(strings.TrimSpace(n), 40)
			}
			if d, _ := envWriter.Payload["strategy_description"].(string); strings.TrimSpace(d) != "" {
				strategyDesc = truncateRunes(strings.TrimSpace(d), 120)
			}
			promptWritten = true
			break
		}
		if attempt == 2 {
			clampWarnings = append(clampWarnings, "提示词未通过程序校验，已保留原提示词: "+strings.Join(verrs, "；"))
			break
		}
		errList, _ := json.Marshal(verrs)
		writerExtra = "\n⚠️ 上一次撰写未通过程序校验，请修复后重新输出完整四段:\n" + string(errList)
	}
	if !promptWritten && remainingBudget(st) <= 0 {
		clampWarnings = append(clampWarnings, "调用预算耗尽，System Prompt 未重新生成，已保留原提示词")
	}

	if !configMerged {
		st.setStatus("failed", "预算耗尽且未获得有效终稿，请增大发言预算后重试")
		return
	}

	st.mu.Lock()
	st.Result = &councilFinalResult{
		Config:                 base,
		StrategyName:           strategyName,
		StrategyDescription:    strategyDesc,
		ScanIntervalSuggestion: scanIntervalSuggestion,
		Reasoning:              reasoning,
		ClampWarnings:          clampWarnings,
		RepairRounds:           repairRounds,
	}
	st.Status = "completed"
	st.Error = ""
	st.mu.Unlock()
}
