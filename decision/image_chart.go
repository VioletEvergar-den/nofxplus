package decision

// ============================================================================
// K线图片模式 - 将主时间框架K线渲染成 PNG 图片供多模态 AI 阅读
// ============================================================================
// 设计哲学：图给形态，字给数字，程序自检，模型交作业
//  1. 图给形态：蜡烛+EMA+BOLL+成交量+MACD 由 chartimg 纯 Go 渲染为 PNG
//  2. 字给数字：图例直接写 EMA20/EMA50/BOLL 最新精确值；文字部分保留指标速览
//  3. 程序自检：渲染后 round-trip 校验（chartimg 内部），失败自动回退文字模式
//  4. 模型交作业：要求模型输出 CHART_READING 行，程序用真实数据判卷打日志
// ============================================================================

import (
	"encoding/base64"
	"fmt"
	"nofx/decision/chartimg"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"regexp"
	"sync"
	"time"
)

// maxImageCharts 单次决策最多渲染的K线图片数（控制 token 消耗，持仓币优先）
// 多周期模式下每币每个勾选周期一张图（如 2 币 × 3 周期 = 6 张）
const maxImageCharts = 9

// ChartReading 模型对K线图的视觉复述（用于程序判卷，判定模型是否真的读图）
type ChartReading struct {
	Trend        string // up / down / flat：主时间框架趋势方向
	EMA20VsEMA50 string // above / below：EMA20 相对 EMA50 位置
	VolumeState  string // expanding / shrinking / flat：近期成交量状态
}

// reChartReading 提取模型输出中的读图作业行，例如：
// CHART_READING: trend=up; ema20_vs_ema50=above; volume=expanding
var reChartReading = regexp.MustCompile(
	`(?i)CHART_READING:\s*trend\s*=\s*(up|down|flat)[;,\s]+ema20_vs_ema50\s*=\s*(above|below)[;,\s]+volume\s*=\s*(expanding|shrinking|flat)`)

// visionUnsupportedClients 记录确认不支持视觉（多模态调用失败）的客户端，按指针缓存
// 同一 client 后续周期直接跳过图片模式，避免每轮都浪费一次失败的调用
var visionUnsupportedClients sync.Map

func markVisionUnsupported(client mcp.AIClient) {
	if client == nil {
		return
	}
	visionUnsupportedClients.Store(fmt.Sprintf("%p", client), struct{}{})
}

func isVisionUnsupported(client mcp.AIClient) bool {
	if client == nil {
		return true
	}
	_, ok := visionUnsupportedClients.Load(fmt.Sprintf("%p", client))
	return ok
}

// extractChartReading 从 AI 原始响应中提取读图作业行（未找到返回 nil）
func extractChartReading(rawResponse string) *ChartReading {
	if m := reChartReading.FindStringSubmatch(rawResponse); len(m) >= 4 {
		return &ChartReading{
			Trend:        m[1],
			EMA20VsEMA50: m[2],
			VolumeState:  m[3],
		}
	}
	return nil
}

// selectImageChartSymbols 选择要渲染成图片的币种：持仓币优先，候选币填充，最多 limit 个
func selectImageChartSymbols(ctx *Context, limit int) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, limit)
	add := func(sym string) {
		if sym == "" || seen[sym] || len(out) >= limit {
			return
		}
		seen[sym] = true
		out = append(out, sym)
	}
	for _, p := range ctx.Positions {
		add(p.Symbol)
	}
	for _, c := range ctx.CandidateCoins {
		add(c.Symbol)
	}
	return out
}

// pickChartTimeframe 选择渲染用时间框架：优先主时间框架，否则取K线最多的时间框架
func pickChartTimeframe(mdata *market.Data, primaryTF string) (string, *market.TimeframeSeriesData) {
	if mdata == nil || mdata.TimeframeData == nil {
		return "", nil
	}
	if primaryTF != "" {
		if d, ok := mdata.TimeframeData[primaryTF]; ok && d != nil && len(d.Klines) > 0 {
			return primaryTF, d
		}
	}
	bestTF := ""
	var best *market.TimeframeSeriesData
	for tf, d := range mdata.TimeframeData {
		if d == nil || len(d.Klines) == 0 {
			continue
		}
		if best == nil || len(d.Klines) > len(best.Klines) {
			bestTF, best = tf, d
		}
	}
	return bestTF, best
}

// renderSymbolChart 将单个币种渲染为 PNG（symbol 单独传参，market.Data 无 Symbol 字段）
func renderSymbolChart(symbol string, mdata *market.Data, timeframe string, tfData *market.TimeframeSeriesData, pos *PositionInfo) ([]byte, error) {
	s := chartimg.Series{
		Symbol:    symbol,
		Timeframe: timeframe,
		LastPrice: mdata.CurrentPrice,
	}
	for _, k := range tfData.Klines {
		s.Candles = append(s.Candles, chartimg.Candle{
			Time:   k.Time / 1000, // KlineBar.Time 为毫秒，渲染用秒
			Open:   k.Open,
			High:   k.High,
			Low:    k.Low,
			Close:  k.Close,
			Volume: k.Volume,
		})
	}
	s.EMA20 = tfData.EMA20Values
	s.EMA50 = tfData.EMA50Values
	s.BOLLUpper = tfData.BOLLUpper
	s.BOLLMiddle = tfData.BOLLMiddle
	s.BOLLLower = tfData.BOLLLower
	s.MACD = tfData.MACDValues
	if len(tfData.Klines) > 0 {
		s.DataTime = time.UnixMilli(tfData.Klines[len(tfData.Klines)-1].Time).UTC()
	}
	// 持仓币：标注进场价水平线
	if pos != nil && pos.EntryPrice > 0 {
		s.Levels = append(s.Levels, chartimg.PriceLevel{Price: pos.EntryPrice, Label: "ENTRY", RGB: 0xeaecef})
	}
	return chartimg.Render(s)
}

// buildImageChartParts 构造多模态 user 消息：
// 文本 part = userPrompt（图片模式已精简K线表）+ 图表说明 + 图片清单 + 读图指令
// 图片 part = 每个币种一张 PNG
// 返回值：parts（发送给AI）、fullText（实际发送的完整文本，含图表说明块，用于前端展示）、imageURLs（PNG data URL 列表，用于前端展示）
// 全部币种渲染失败时返回 error，调用方回退纯文字模式
func buildImageChartParts(ctx *Context, userPrompt, primaryTF, lang string) ([]mcp.ContentPart, string, []string, error) {
	symbols := selectImageChartSymbols(ctx, maxImageCharts)
	if len(symbols) == 0 {
		return nil, "", nil, fmt.Errorf("无可渲染币种（无持仓且无候选币）")
	}

	type renderedChart struct {
		symbol    string
		timeframe string
		dataURL   string
	}
	var charts []renderedChart
	posIndex := make(map[string]*PositionInfo)
	for i := range ctx.Positions {
		posIndex[ctx.Positions[i].Symbol] = &ctx.Positions[i]
	}

	for _, sym := range symbols {
		mdata := ctx.MarketDataMap[sym]
		if mdata == nil || mdata.TimeframeData == nil {
			continue
		}
		// 每个勾选周期各渲染一张图（如勾选 15m/1h/4h → 每币 3 张）
		tfs := ctx.Timeframes
		if len(tfs) == 0 {
			// 兜底：未提供周期列表时按主周期渲染单图
			if tf, tfData := pickChartTimeframe(mdata, primaryTF); tfData != nil {
				tfs = []string{tf}
			}
		}
		for _, tf := range tfs {
			if len(charts) >= maxImageCharts {
				break
			}
			tfData := mdata.TimeframeData[tf]
			if tfData == nil || len(tfData.Klines) == 0 {
				continue
			}
			png, err := renderSymbolChart(sym, mdata, tf, tfData, posIndex[sym])
			if err != nil {
				logger.Infof("⚠️ [图片模式] %s %s 渲染失败: %v", sym, tf, err)
				continue
			}
			charts = append(charts, renderedChart{
				symbol:    sym,
				timeframe: tf,
				dataURL:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
			})
		}
	}
	if len(charts) == 0 {
		return nil, "", nil, fmt.Errorf("所有币种K线渲染失败")
	}

	// 图片清单
	var listStr string
	for i, c := range charts {
		listStr += fmt.Sprintf("%d. %s（%s 时间框架）\n", i+1, c.symbol, c.timeframe)
	}

	text := userPrompt + "\n\n---\n\n" + buildChartImageIntro(lang, len(charts), listStr)

	parts := make([]mcp.ContentPart, 0, len(charts)+1)
	parts = append(parts, mcp.NewTextPart(text))
	imageURLs := make([]string, 0, len(charts))
	for _, c := range charts {
		parts = append(parts, mcp.NewImagePart(c.dataURL))
		imageURLs = append(imageURLs, c.dataURL)
	}
	return parts, text, imageURLs, nil
}

// buildChartImageIntro 图表说明 + 图片清单 + 读图指令（双语）
func buildChartImageIntro(lang string, count int, listStr string) string {
	if lang == string(LangChinese) {
		return fmt.Sprintf(`## 📈 K线图表（图片模式，共 %d 张）

%s
以下图片由程序根据真实行情数据渲染（币安深色配色）：
- **红色蜡烛 = 阳线（收涨），绿色蜡烛 = 阴线（收跌）**（UP=RED, DOWN=GREEN）
- 主图：蜡烛 + EMA20（琥珀色） + EMA50（紫色） + 布林带（灰色），主图内白色文字标注了图中最高价（H=）与最低价（L=），持仓币种白色虚线为进场价（ENTRY）
- 中部副图：成交量（右轴有最大量/半量刻度）；底部副图：MACD（青色线，绕零轴，右轴有 ±峰值刻度）
- 右侧图例写有 EMA20 / EMA50 / BOLL 上中下轨的**最新精确数值**，右轴为价格刻度
- 多张图片对应不同时间框架（每张图标题含币种与周期），请逐张做周期对比

图片只提供形态信息；精确数值（RSI、ATR、图例数值等）以文字部分为准。

### 读图作业（必须完成）
分析完图片后，你必须在 <reasoning> 的**最后一行**原样输出（程序会核对，请如实读图）：
CHART_READING: trend=<up|down|flat>; ema20_vs_ema50=<above|below>; volume=<expanding|shrinking|flat>
含义：主时间框架趋势方向 / EMA20 相对 EMA50 的位置 / 近期成交量相对之前的状态。`, count, listStr)
	}
	return fmt.Sprintf(`## 📈 K-line Charts (image mode, %d images)

%s
The following images are rendered by the program from real market data (Binance dark theme):
- **Red candle = bullish (close up), green candle = bearish (close down)** (UP=RED, DOWN=GREEN)
- Main pane: candles + EMA20 (amber) + EMA50 (purple) + Bollinger Bands (gray); white text marks the highest price (H=) and lowest price (L=) in the chart; for held positions the white dashed line marks the entry price (ENTRY)
- Middle pane: volume (right axis shows max/half-volume ticks); bottom pane: MACD (teal line, around zero axis, right axis shows +/- peak ticks)
- The legend on the right shows the **latest exact values** of EMA20 / EMA50 / BOLL upper/middle/lower; the right axis is the price scale
- Multiple images correspond to different timeframes (each image title contains symbol and timeframe); compare across timeframes one by one

Images provide shape information only; exact numbers (RSI, ATR, legend values) are authoritative in the text section.

### Chart reading assignment (required)
After analyzing the images, you MUST output as the **last line** of your <reasoning> (the program will verify it, read the chart honestly):
CHART_READING: trend=<up|down|flat>; ema20_vs_ema50=<above|below>; volume=<expanding|shrinking|flat>
Meaning: primary timeframe trend direction / EMA20 relative to EMA50 / recent volume state versus before.`, count, listStr)
}

// gradeImageChartReadings 用真实数据判卷模型的读图作业（仅打日志，不影响决策）
func gradeImageChartReadings(decision *FullDecision, ctx *Context, primaryTF string) {
	if decision == nil || decision.ChartReading == nil || len(ctx.ImageChartSymbols) == 0 {
		return
	}
	for sym := range ctx.ImageChartSymbols {
		mdata := ctx.MarketDataMap[sym]
		if mdata == nil {
			continue
		}
		tf, tfData := pickChartTimeframe(mdata, primaryTF)
		if tfData == nil {
			continue
		}
		for _, line := range gradeChartReading(*decision.ChartReading, tfData) {
			logger.Infof("📊 [图片模式判卷] %s(%s): %s", sym, tf, line)
		}
	}
}

// gradeChartReading 逐项对比模型读图结果与程序计算的真实值
func gradeChartReading(r ChartReading, tfData *market.TimeframeSeriesData) []string {
	n := len(tfData.Klines)
	if n < 12 {
		return []string{"⚠️ K线不足12根，跳过判卷"}
	}
	var out []string

	// 1. 趋势：最后收盘 vs 10根前收盘（阈值 ±0.15%）
	last := tfData.Klines[n-1].Close
	base := tfData.Klines[n-11].Close
	if base > 0 {
		chg := (last - base) / base * 100
		expect := "flat"
		if chg >= 0.15 {
			expect = "up"
		} else if chg <= -0.15 {
			expect = "down"
		}
		out = append(out, gradeItem("trend", r.Trend, expect, fmt.Sprintf("近10根收盘变化 %+.2f%%", chg)))
	}

	// 2. EMA20 vs EMA50：最新值比较
	if len(tfData.EMA20Values) > 0 && len(tfData.EMA50Values) > 0 {
		e20 := tfData.EMA20Values[len(tfData.EMA20Values)-1]
		e50 := tfData.EMA50Values[len(tfData.EMA50Values)-1]
		if e50 > 0 && e20 > 0 {
			expect := "above"
			if e20 < e50 {
				expect = "below"
			}
			out = append(out, gradeItem("ema20_vs_ema50", r.EMA20VsEMA50, expect, fmt.Sprintf("EMA20=%s EMA50=%s", formatPriceSmart(e20), formatPriceSmart(e50))))
		}
	}

	// 3. 成交量：最后3根均量 vs 之前20根均量（>1.2 放量，<0.8 缩量）
	recentEnd := n
	recentStart := n - 3
	prevEnd := recentStart
	prevStart := prevEnd - 20
	if prevStart < 0 {
		prevStart = 0
	}
	if prevEnd > prevStart {
		recentSum, prevSum := 0.0, 0.0
		for i := recentStart; i < recentEnd; i++ {
			recentSum += tfData.Klines[i].Volume
		}
		for i := prevStart; i < prevEnd; i++ {
			prevSum += tfData.Klines[i].Volume
		}
		recentAvg := recentSum / float64(recentEnd-recentStart)
		prevAvg := prevSum / float64(prevEnd-prevStart)
		if prevAvg > 0 {
			ratio := recentAvg / prevAvg
			expect := "flat"
			if ratio >= 1.2 {
				expect = "expanding"
			} else if ratio <= 0.8 {
				expect = "shrinking"
			}
			out = append(out, gradeItem("volume", r.VolumeState, expect, fmt.Sprintf("近3根均量/前20根均量 = %.2f", ratio)))
		}
	}
	return out
}

// gradeItem 单项判卷结果
func gradeItem(item, got, expect, detail string) string {
	mark := "✓"
	if got != expect {
		mark = "✗"
	}
	return fmt.Sprintf("%s %s: 模型=%s 程序=%s (%s)", mark, item, got, expect, detail)
}
