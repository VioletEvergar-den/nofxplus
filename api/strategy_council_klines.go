package api

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nofx/market"
)

// 多周期 K 线观察输入：为「周期指标工程师」提供周K/日K/4小时K 三套独立数据。
// 每个周期单独一节（区间统计 + 指标快照 + 最近 K 线简表），要求该专家逐周期分别给出趋势判断。

var councilKlineTimeframes = []struct {
	Interval string
	Limit    int
	Label    string
}{
	{"1w", 40, "周K线（每根=1周）"},
	{"1d", 60, "日K线（每根=1天）"},
	{"4h", 60, "4小时K线（每根=4小时）"},
}

var councilSymbolPattern = regexp.MustCompile(`[A-Z]{2,10}USDT`)

// extractCouncilSymbol 从用户意图中提取币种交易对（如 ETHUSDT），未提到则默认 BTCUSDT
func extractCouncilSymbol(intent string) string {
	if s := councilSymbolPattern.FindString(strings.ToUpper(intent)); s != "" {
		return s
	}
	return "BTCUSDT"
}

// buildMultiTimeframeKlineSection 拉取三个周期 K 线并各自独立成节
func buildMultiTimeframeKlineSection(symbol string) string {
	client := market.NewAPIClient()
	var sb strings.Builder
	fetched := 0
	for _, tf := range councilKlineTimeframes {
		klines, err := client.GetKlines(symbol, tf.Interval, tf.Limit)
		if err != nil || len(klines) < 30 {
			fmt.Fprintf(&sb, "### %s %s\n数据获取失败，分析时跳过该周期。\n\n", symbol, tf.Label)
			continue
		}
		fetched++
		writeCouncilTimeframeSection(&sb, symbol, tf.Interval, tf.Label, klines)
	}
	if fetched == 0 {
		return ""
	}
	return sb.String()
}

// writeCouncilTimeframeSection 输出单个周期的一节数据
func writeCouncilTimeframeSection(sb *strings.Builder, symbol, interval, label string, klines []market.Kline) {
	n := len(klines)
	last := klines[n-1]

	// 区间统计（取最近 30 根）
	spanStart := 0
	if n > 30 {
		spanStart = n - 30
	}
	span := klines[spanStart:]
	firstClose := span[0].Close
	high, low, volSum := span[0].High, span[0].Low, 0.0
	for _, k := range span {
		if k.High > high {
			high = k.High
		}
		if k.Low < low {
			low = k.Low
		}
		volSum += k.Volume
	}
	chgPct := (last.Close - firstClose) / firstClose * 100
	volAvg := volSum / float64(len(span))

	closes := make([]float64, n)
	for i, k := range klines {
		closes[i] = k.Close
	}

	ema20 := councilEMA(closes, 20)
	ema50 := councilEMA(closes, 50)
	rsi14 := councilRSI(closes, 14)
	dif, dea, hist := councilMACD(closes)
	atr14 := councilATR(klines, 14)

	emaBias := "EMA20 ≥ EMA50（多头排列）"
	if ema20 < ema50 {
		emaBias = "EMA20 < EMA50（空头排列）"
	}
	macdDir := "DIF 在 DEA 上方（多头动能）"
	if dif < dea {
		macdDir = "DIF 在 DEA 下方（空头动能）"
	}

	fmt.Fprintf(sb, "### %s %s（最近一根开盘时间 %s UTC）\n", symbol, label, time.UnixMilli(last.OpenTime).UTC().Format("2006-01-02 15:04"))
	fmt.Fprintf(sb, "- 最新收盘: %s\n", formatCouncilPrice(last.Close))
	fmt.Fprintf(sb, "- 近30根区间: 涨跌幅 %+.2f%%，最高 %s，最低 %s，均量 %.2f\n", chgPct, formatCouncilPrice(high), formatCouncilPrice(low), volAvg)
	fmt.Fprintf(sb, "- 指标快照: EMA20=%s，EMA50=%s（%s）；RSI14=%.1f；MACD DIF=%s，DEA=%s，柱=%s（%s）；ATR14=%s\n",
		formatCouncilPrice(ema20), formatCouncilPrice(ema50), emaBias, rsi14,
		formatCouncilPrice(dif), formatCouncilPrice(dea), formatCouncilPrice(hist), macdDir, formatCouncilPrice(atr14))
	fmt.Fprintf(sb, "- 最近5根K线（开/高/低/收/量）:\n")
	for i := n - 5; i < n; i++ {
		k := klines[i]
		fmt.Fprintf(sb, "  - %s: %s / %s / %s / %s / %.2f\n",
			time.UnixMilli(k.OpenTime).UTC().Format("01-02 15:04"),
			formatCouncilPrice(k.Open), formatCouncilPrice(k.High), formatCouncilPrice(k.Low), formatCouncilPrice(k.Close), k.Volume)
	}
	sb.WriteString("\n")
}

// formatCouncilPrice 按价格量级自适应小数位
func formatCouncilPrice(v float64) string {
	abs := math.Abs(v)
	switch {
	case abs >= 100:
		return strconv.FormatFloat(v, 'f', 2, 64)
	case abs >= 1:
		return strconv.FormatFloat(v, 'f', 4, 64)
	case abs == 0:
		return "0"
	default:
		return strconv.FormatFloat(v, 'f', 6, 64)
	}
}

// councilEMA 与 market.calculateEMA 同口径：SMA 起步，逐根递推
func councilEMA(closes []float64, period int) float64 {
	if len(closes) < period || period <= 0 {
		return 0
	}
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += closes[i]
	}
	ema := sum / float64(period)
	mult := 2.0 / float64(period+1)
	for i := period; i < len(closes); i++ {
		ema = (closes[i]-ema)*mult + ema
	}
	return ema
}

// councilEMASeries 完整 EMA 序列（用于 MACD 的 DIF/DEA）
func councilEMASeries(closes []float64, period int) []float64 {
	out := make([]float64, len(closes))
	if len(closes) == 0 || period <= 0 {
		return out
	}
	mult := 2.0 / float64(period+1)
	out[0] = closes[0]
	for i := 1; i < len(closes); i++ {
		out[i] = (closes[i]-out[i-1])*mult + out[i-1]
	}
	return out
}

// councilMACD 返回最新 DIF/DEA/HIST
func councilMACD(closes []float64) (dif, dea, hist float64) {
	if len(closes) < 26 {
		return 0, 0, 0
	}
	fast := councilEMASeries(closes, 12)
	slow := councilEMASeries(closes, 26)
	difSeries := make([]float64, len(closes))
	for i := range closes {
		difSeries[i] = fast[i] - slow[i]
	}
	deaSeries := councilEMASeries(difSeries, 9)
	i := len(closes) - 1
	return difSeries[i], deaSeries[i], difSeries[i] - deaSeries[i]
}

// councilRSI 与 market.calculateRSI 同口径（Wilder 平滑）
func councilRSI(closes []float64, period int) float64 {
	if len(closes) <= period {
		return 0
	}
	gains, losses := 0.0, 0.0
	for i := 1; i <= period; i++ {
		ch := closes[i] - closes[i-1]
		if ch > 0 {
			gains += ch
		} else {
			losses += -ch
		}
	}
	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)
	for i := period + 1; i < len(closes); i++ {
		ch := closes[i] - closes[i-1]
		if ch > 0 {
			avgGain = (avgGain*float64(period-1) + ch) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) + (-ch)) / float64(period)
		}
	}
	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs)
}

// councilATR 与 market.calculateATR 同口径（Wilder 平滑）
func councilATR(klines []market.Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}
	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		h, l, pc := klines[i].High, klines[i].Low, klines[i-1].Close
		trs[i] = math.Max(h-l, math.Max(math.Abs(h-pc), math.Abs(l-pc)))
	}
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)
	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}
	return atr
}
