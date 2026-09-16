package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"nofx/logger"
	"nofx/market"
	"nofx/store"
)

// ---------- 受控自主 Agent 圆桌 ----------
// 5 个角色串行圆桌，前序发言全量可见（Transcript 消息流）；
// 角色可自主调用工具（K线/新闻），风控评审官可点名追问；
// 全场 AI 调用受用户设定的总预算约束，每次发言注入剩余预算。

var reToolBlock = regexp.MustCompile("(?s)```tool\\s*(\\{.*?\\})\\s*```")
var reAskBlock = regexp.MustCompile("(?s)```ask\\s*(\\{.*?\\})\\s*```")

const councilToolProtocol = `
## 工具（可自主调用，数据由系统实时拉取）
需要数据时输出一个独立代码块（格式如下），系统执行后把结果回传给你继续分析：
` + "```tool" + `
{"tool": "get_klines", "symbol": "ETHUSDT", "interval": "4h", "limit": 60}
` + "```" + `
- get_klines：K线查询。symbol（带 USDT 后缀）、interval（5m/15m/30m/1h/4h/1d/1w）、limit（10~200）。返回区间统计+指标快照+最近K线明细。
` + "```tool" + `
{"tool": "search_news", "query": "ETH ETF 最新消息"}
` + "```" + `
- search_news：联网搜索最新资讯（带发布日期，已过滤30天前旧闻）
- 连续工具调用最多 4 次；结论必须有数据支撑，请主动查证。`

const councilAskProtocol = `
## 点名追问机制
如需某个角色澄清或补充论据，输出一个独立代码块（每轮最多追问 1 次）：
` + "```ask" + `
{"target": "chief_trader", "question": "你的止损逻辑在单边极端行情下如何自处？"}
` + "```" + `
合法 target: intel_analyst / chief_trader / strategy_architect。系统会把问题转给对方在圆桌作答后回到你。`

// councilAgentSystemPrompt Agent 角色系统提示词
func councilAgentSystemPrompt(role councilRoleDef, lang string) string {
	summaryLang := "中文"
	if lang == "en" {
		summaryLang = "English"
	}
	base := fmt.Sprintf(`你是 NOFX 量化交易系统「策略专家团」成员：%s%s（Agent 模式）。团队围绕用户策略意图圆桌协作，按顺序发言，所有前序发言在「圆桌发言记录」中全量可见——引用或反驳他人观点时请点名。

## 输出要求（严格遵守）
最终结论只输出一个 JSON 对象（信封格式），不要输出任何其他文字：
%s

- summary 和 concerns 用 %s。
- payload 字段名和枚举值必须严格使用下方给定的英文值。
- 对上游结论有不同意见必须写入 concerns，供风控评审官裁决。
- 需要数据时先输出工具代码块；需要追问时输出 ask 代码块；然后系统会回传结果。
`, role.Emoji, roleDisplayName(role.ID, lang), envelopeSchemaHint, summaryLang)

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
你是专家团的首席笔杆子：基于全部圆桌发言（交易计划/参数配置/终审裁决），为交易系统撰写一套完整、可执行的 System Prompt 策略。这份提示词将直接作为 AI 交易员的行为准则，质量标准是「拿来即可实盘」。

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
	}
	return base
}

// addTranscript 追加圆桌发言记录（并发安全）
func (st *councilState) addTranscript(role, emoji, name, kind, content string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Transcript = append(st.Transcript, councilTranscriptEntry{
		Role: role, Emoji: emoji, Name: name, Kind: kind,
		Content: content, CreatedAt: time.Now(),
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
			fmt.Fprintf(&sb, "【%s %s 发言】\n%s\n\n", e.Emoji, e.Name, e.Content)
		case "tool_call":
			fmt.Fprintf(&sb, "【%s %s 调用工具】%s\n", e.Emoji, e.Name, e.Content)
		case "tool_result":
			fmt.Fprintf(&sb, "【工具结果】\n%s\n\n", e.Content)
		case "question":
			fmt.Fprintf(&sb, "%s\n", e.Content)
		case "answer":
			fmt.Fprintf(&sb, "【%s %s 回应】\n%s\n\n", e.Emoji, e.Name, e.Content)
		default:
			fmt.Fprintf(&sb, "【%s %s】%s\n\n", e.Emoji, e.Name, e.Content)
		}
	}
	return sb.String()
}

// truncateRunes 按 rune 截断
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…(截断)"
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
	default:
		return "错误: 未知工具 " + name
	}
}

// parseToolCalls 提取响应中的工具调用块
func parseToolCalls(resp string) []map[string]any {
	matches := reToolBlock.FindAllStringSubmatch(resp, -1)
	var calls []map[string]any
	for _, m := range matches {
		var c map[string]any
		if err := json.Unmarshal([]byte(m[1]), &c); err == nil {
			if _, ok := c["tool"].(string); ok {
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
		resp, err := s.callCouncilAI(st.UserID, modelID, councilAgentSystemPrompt(role, st.Language), sb.String())
		if err != nil {
			st.mu.Lock()
			step.Status = string(councilStepFailed)
			step.Error = err.Error()
			st.mu.Unlock()
			return nil, err
		}

		// 1) 工具调用（仅数据型角色：情报分析师/交易员/架构师）
		canTool := roleID == "intel_analyst" || roleID == "chief_trader" || roleID == "strategy_architect"
		if calls := parseToolCalls(resp); len(calls) > 0 && canTool && toolUses < 4 && remain > 2 {
			for _, c := range calls {
				toolName, _ := c["tool"].(string)
				desc := toolName
				if toolName == "get_klines" {
					desc = fmt.Sprintf("get_klines %v %v×%v", c["symbol"], c["interval"], c["limit"])
				} else if toolName == "search_news" {
					desc = fmt.Sprintf("search_news %v", c["query"])
				}
				st.addTranscript(role.ID, role.Emoji, roleDisplayName(role.ID, st.Language), "tool_call", desc)
				st.addActivity(role.ID, "🔧 "+desc)
				result := s.execCouncilTool(c, st.Language)
				st.addTranscript("system", "📊", "System", "tool_result", truncateRunes(result, 3000))
			}
			toolUses += len(calls)
			continue
		}

		// 2) 点名追问（仅风控评审官）
		if m := reAskBlock.FindStringSubmatch(resp); m != nil && roleID == "risk_reviewer" && !askUsed && remainingBudget(st) >= 2 {
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
					st.addTranscript(roleID, role.Emoji, roleDisplayName(roleID, st.Language), "question",
						fmt.Sprintf("【⚖️ 风控评审官 → %s %s 追问】%s", tRole.Emoji, roleDisplayName(tRole.ID, st.Language), ask.Question))
					st.addActivity(roleID, "❓ 追问 "+roleDisplayName(tRole.ID, st.Language)+": "+truncateRunes(ask.Question, 60))
					// 被点名角色作答一轮（一次性，信封格式）
					if remainingBudget(st) > 0 {
						atomic.AddInt32(&st.UsedBudget, 1)
						ansPrompt := fmt.Sprintf("## 用户意图\n%s\n\n## 圆桌发言记录\n%s\n## 追问\n风控评审官向你提问：%s\n\n请直接作答（输出信封 JSON，summary 为你的圆桌回应内容）。", st.Intent, buildTranscriptText(st), ask.Question)
						resp2, err2 := s.callCouncilAI(st.UserID, modelID, councilAgentSystemPrompt(tRole, st.Language), ansPrompt)
						if err2 == nil {
							if env2, errP := parseEnvelope(resp2); errP == nil {
								content := env2.Summary
								if len(env2.Concerns) > 0 {
									content += "\n（疑虑: " + strings.Join(env2.Concerns, "；") + "）"
								}
								st.addTranscript(tRole.ID, tRole.Emoji, roleDisplayName(tRole.ID, st.Language), "answer", content)
								st.addActivity(tRole.ID, "💬 回应追问")
							}
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
			// 格式错误：给一次重试机会
			if turn < 8 {
				st.addTranscript("system", "⚠️", "System", "system", "输出格式错误（"+errP.Error()+"），请立即重新输出信封 JSON。")
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
		if pj, errJ := json.Marshal(env.Payload); errJ == nil {
			content += "\npayload: " + truncateRunes(string(pj), 1800)
		}
		st.addTranscript(roleID, role.Emoji, roleDisplayName(roleID, st.Language), "speech", content)
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
	st.addTranscript("system", "🏛️", "System", "system", "策略意图: "+st.Intent)

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
	if _, err := s.runAgentTurn(st, modelID, "chief_trader", "请基于圆桌记录与真实K线给出实战交易计划。"); err != nil {
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
	if _, err := s.runAgentTurn(st, modelID, "strategy_architect", "请给出完整参数配置（含 1w/1d/4h 逐周期趋势判断、K线与指标、币种来源）。"); err != nil {
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
		instr := "请审查前序全部发言，给出风控意见与最终策略配置终稿（final_config）。"
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
	writerExtra := ""
	for attempt := 0; attempt < 3; attempt++ {
		if st.cancelFlag.Load() {
			st.setStatus("cancelled", "")
			return
		}
		if remainingBudget(st) <= 0 {
			break
		}
		instr := "请基于全部圆桌发言撰写完整的 System Prompt 策略（四段结构）。"
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
		secs, verrs := extractWriterSections(envWriter.Payload)
		if len(verrs) == 0 {
			base.PromptSections.RoleDefinition = secs["role_definition"]
			base.PromptSections.TradingFrequency = secs["trading_frequency"]
			base.PromptSections.EntryStandards = secs["entry_standards"]
			base.PromptSections.DecisionProcess = secs["decision_process"]
			if cp, _ := envWriter.Payload["custom_prompt"].(string); strings.TrimSpace(cp) != "" {
				base.CustomPrompt = strings.TrimSpace(cp)
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
		ScanIntervalSuggestion: scanIntervalSuggestion,
		Reasoning:              reasoning,
		ClampWarnings:          clampWarnings,
		RepairRounds:           repairRounds,
	}
	st.Status = "completed"
	st.Error = ""
	st.mu.Unlock()
}
