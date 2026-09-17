package decision

import (
	"fmt"
	"math"
	"nofx/market"
	"nofx/store"
	"sort"
	"strconv"
	"strings"
	"time"
)

// formatPriceSmart 根据数值量级自适应小数位，避免小价格币种（如 DOGE 0.08x）被固定位数抹平
// ≥100: 2 位；1~100: 4 位；0.01~1: 5 位；<0.01: 6 位（自动处理负数，适用于 MACD 等可负指标）
func formatPriceSmart(v float64) string {
	abs := math.Abs(v)
	switch {
	case abs >= 100:
		return strconv.FormatFloat(v, 'f', 2, 64)
	case abs >= 1:
		return strconv.FormatFloat(v, 'f', 4, 64)
	case abs >= 0.01:
		return strconv.FormatFloat(v, 'f', 5, 64)
	default:
		return strconv.FormatFloat(v, 'f', 6, 64)
	}
}

// ============================================================================
// AI Data Formatter - AI数据格式化器
// ============================================================================
// 将交易上下文转换为AI友好的格式，确保AI能够100%理解数据
// ============================================================================

// ========== 中文格式化函数 ==========

// formatAccountZH 格式化账户信息（中文）
func formatAccountZH(ctx *Context) string {
	acc := ctx.Account
	var sb strings.Builder

	sb.WriteString("## 账户状态\n\n")
	sb.WriteString(fmt.Sprintf("总权益: %.2f USDT | ", acc.TotalEquity))
	sb.WriteString(fmt.Sprintf("可用余额: %.2f USDT (%.1f%%) | ", acc.AvailableBalance, (acc.AvailableBalance/acc.TotalEquity)*100))
	sb.WriteString(fmt.Sprintf("总盈亏: %+.2f%% | ", acc.TotalPnLPct))
	sb.WriteString(fmt.Sprintf("保证金使用率: %.1f%% | ", acc.MarginUsedPct))
	sb.WriteString(fmt.Sprintf("持仓数: %d\n\n", acc.PositionCount))

	// 添加风险提示
	if acc.MarginUsedPct > 85 {
		sb.WriteString("⚠️ **风险警告**: 保证金使用率 > 85%，处于高风险状态！\n\n")
	} else if acc.MarginUsedPct > 50 {
		sb.WriteString("⚠️ **风险提示**: 保证金使用率 > 50%，建议谨慎开仓\n\n")
	}

	return sb.String()
}

// formatRecentTradesZH 格式化最近交易（中文）
func formatRecentTradesZH(orders []RecentOrder) string {
	var sb strings.Builder
	sb.WriteString("## 最近完成的交易\n\n")

	for i, order := range orders {
		// 判断盈亏
		profitOrLoss := "盈利"
		if order.RealizedPnL < 0 {
			profitOrLoss = "亏损"
		}

		sb.WriteString(fmt.Sprintf("%d. %s %s | 进场 %s 出场 %s | %s: %+.2f USDT (%+.2f%%) | %s → %s (%s)\n",
			i+1,
			order.Symbol,
			order.Side,
			formatPriceSmart(order.EntryPrice),
			formatPriceSmart(order.ExitPrice),
			profitOrLoss,
			order.RealizedPnL,
			order.PnLPct,
			order.EntryTime,
			order.ExitTime,
			order.HoldDuration,
		))
	}

	sb.WriteString("\n")
	return sb.String()
}

// formatCurrentPositionsZH 格式化当前持仓（中文）
func formatCurrentPositionsZH(strategy_config *store.StrategyConfig, ctx *Context) string {
	var sb strings.Builder
	sb.WriteString("## 当前持仓\n\n")

	for i, pos := range ctx.Positions {
		// 计算回撤
		drawdown := pos.UnrealizedPnLPct - pos.PeakPnLPct

		sb.WriteString(fmt.Sprintf("%d. %s %s | ", i+1, pos.Symbol, strings.ToUpper(pos.Side)))
		sb.WriteString(fmt.Sprintf("进场 %s 当前 %s | ", formatPriceSmart(pos.EntryPrice), formatPriceSmart(pos.MarkPrice)))
		sb.WriteString(fmt.Sprintf("数量 %.3f | ", pos.Quantity))
		sb.WriteString(fmt.Sprintf("仓位价值 %.2f USDT | ", pos.Quantity*pos.MarkPrice))
		sb.WriteString(fmt.Sprintf("盈亏 %+.2f%% | ", pos.UnrealizedPnLPct))
		sb.WriteString(fmt.Sprintf("盈亏金额 %+.2f USDT | ", pos.UnrealizedPnL))
		sb.WriteString(fmt.Sprintf("峰值盈亏 %.2f%% | ", pos.PeakPnLPct))
		sb.WriteString(fmt.Sprintf("杠杆 %dx | ", pos.Leverage))
		sb.WriteString(fmt.Sprintf("保证金 %.0f USDT | ", pos.MarginUsed))
		sb.WriteString(fmt.Sprintf("强平价 %s\n", formatPriceSmart(pos.LiquidationPrice)))

		// 添加分析提示
		if drawdown < -0.30*pos.PeakPnLPct && pos.PeakPnLPct > 0.02 {
			sb.WriteString(fmt.Sprintf("   ⚠️ **止盈提示**: 当前盈亏从峰值 %.2f%% 回撤到 %.2f%%，回撤幅度 %.2f%%，建议考虑止盈\n",
				pos.PeakPnLPct, pos.UnrealizedPnLPct, (drawdown/pos.PeakPnLPct)*100))
		}

		if pos.UnrealizedPnLPct < -4.0 {
			sb.WriteString("   ⚠️ **止损提示**: 亏损接近-5%止损线，建议考虑止损\n")
		}

		// 显示当前价格（如果有市场数据）
		if ctx.MarketDataMap != nil {
			if mdata, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(formatMarketDataZH(strategy_config, mdata))
			}
		}

		// 市场微观结构数据（如果有）
		if ctx.MicrostructureDataMap != nil {
			if ms, ok := ctx.MicrostructureDataMap[pos.Symbol]; ok {
				sb.WriteString(formatMicrostructureZH(ms))
			}
		}

		// 量化数据分析提示 (如果有)
		if ctx.QuantDataMap != nil {
			if qdata, ok := ctx.QuantDataMap[pos.Symbol]; ok {
				sb.WriteString(formatQuantDataZH(qdata))
			}
		}

		sb.WriteString("\n")
	}

	return sb.String()
}

// formatCandidateCoinsZH 格式化候选币种（中文）
func formatCandidateCoinsZH(ctx *Context) string {
	var sb strings.Builder
	sb.WriteString("## 候选币种\n\n")

	for i, coin := range ctx.CandidateCoins {
		sb.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, coin.Symbol))

		// 当前价格
		if ctx.MarketDataMap != nil {
			if mdata, ok := ctx.MarketDataMap[coin.Symbol]; ok {
				sb.WriteString(fmt.Sprintf("当前价格: %s\n\n", formatPriceSmart(mdata.CurrentPrice)))

				// K线数据（多时间框架）；图片模式下省略文字K线表，只保留指标最新值速览（形态信息由K线图片提供）
				if mdata.TimeframeData != nil {
					if ctx.ImageChartMode {
						sb.WriteString(formatIndicatorSnapshotZH(coin.Symbol, mdata.TimeframeData, ctx.Timeframes))
					} else {
						sb.WriteString(formatKlineDataZH(coin.Symbol, mdata.TimeframeData, ctx.Timeframes))
					}
				}
			}
		}

		// 量化数据分析提示 (如果有)
		if ctx.QuantDataMap != nil {
			if qdata, ok := ctx.QuantDataMap[coin.Symbol]; ok {
				sb.WriteString(formatQuantDataZH(qdata))
			}
		}
	}

	return sb.String()
}

// formatIndicatorSnapshotZH 图片模式下的指标最新值速览（省略完整序列，形态信息由K线图片提供）
func formatIndicatorSnapshotZH(symbol string, tfData map[string]*market.TimeframeSeriesData, timeframes []string) string {
	var sb strings.Builder
	for _, tf := range timeframes {
		data, ok := tfData[tf]
		if !ok || data == nil || len(data.Klines) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("#### %s %s 指标速览（K线形态见图片，完整序列已省略）\n", symbol, tf))
		var parts []string
		n := len(data.Klines)
		lastClose := formatPriceSmart(data.Klines[n-1].Close)
		parts = append(parts, fmt.Sprintf("最新收盘: %s", lastClose))
		if len(data.RSI7Values) > 0 {
			parts = append(parts, fmt.Sprintf("RSI7: %.1f", data.RSI7Values[len(data.RSI7Values)-1]))
		}
		if len(data.RSI14Values) > 0 {
			parts = append(parts, fmt.Sprintf("RSI14: %.1f", data.RSI14Values[len(data.RSI14Values)-1]))
		}
		if data.ATR14 > 0 {
			parts = append(parts, fmt.Sprintf("ATR14: %s", formatPriceSmart(data.ATR14)))
		}
		if len(data.MACDValues) > 0 {
			parts = append(parts, fmt.Sprintf("MACD: %s", formatPriceSmart(data.MACDValues[len(data.MACDValues)-1])))
		}
		if len(data.EMA20Values) > 0 {
			parts = append(parts, fmt.Sprintf("EMA20: %s", formatPriceSmart(data.EMA20Values[len(data.EMA20Values)-1])))
		}
		if len(data.EMA50Values) > 0 {
			parts = append(parts, fmt.Sprintf("EMA50: %s", formatPriceSmart(data.EMA50Values[len(data.EMA50Values)-1])))
		}
		if len(data.BOLLUpper) > 0 && len(data.BOLLLower) > 0 {
			parts = append(parts, fmt.Sprintf("BOLL上/下轨: %s / %s",
				formatPriceSmart(data.BOLLUpper[len(data.BOLLUpper)-1]),
				formatPriceSmart(data.BOLLLower[len(data.BOLLLower)-1])))
		}
		sb.WriteString(strings.Join(parts, " | ") + "\n\n")
	}
	return sb.String()
}

// formatIndicatorSnapshotEN indicator snapshot for image mode (English)
func formatIndicatorSnapshotEN(symbol string, tfData map[string]*market.TimeframeSeriesData, timeframes []string) string {
	var sb strings.Builder
	for _, tf := range timeframes {
		data, ok := tfData[tf]
		if !ok || data == nil || len(data.Klines) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("#### %s %s indicator snapshot (chart shape in image, full series omitted)\n", symbol, tf))
		var parts []string
		n := len(data.Klines)
		lastClose := formatPriceSmart(data.Klines[n-1].Close)
		parts = append(parts, fmt.Sprintf("Last close: %s", lastClose))
		if len(data.RSI7Values) > 0 {
			parts = append(parts, fmt.Sprintf("RSI7: %.1f", data.RSI7Values[len(data.RSI7Values)-1]))
		}
		if len(data.RSI14Values) > 0 {
			parts = append(parts, fmt.Sprintf("RSI14: %.1f", data.RSI14Values[len(data.RSI14Values)-1]))
		}
		if data.ATR14 > 0 {
			parts = append(parts, fmt.Sprintf("ATR14: %s", formatPriceSmart(data.ATR14)))
		}
		if len(data.MACDValues) > 0 {
			parts = append(parts, fmt.Sprintf("MACD: %s", formatPriceSmart(data.MACDValues[len(data.MACDValues)-1])))
		}
		if len(data.EMA20Values) > 0 {
			parts = append(parts, fmt.Sprintf("EMA20: %s", formatPriceSmart(data.EMA20Values[len(data.EMA20Values)-1])))
		}
		if len(data.EMA50Values) > 0 {
			parts = append(parts, fmt.Sprintf("EMA50: %s", formatPriceSmart(data.EMA50Values[len(data.EMA50Values)-1])))
		}
		if len(data.BOLLUpper) > 0 && len(data.BOLLLower) > 0 {
			parts = append(parts, fmt.Sprintf("BOLL up/low: %s / %s",
				formatPriceSmart(data.BOLLUpper[len(data.BOLLUpper)-1]),
				formatPriceSmart(data.BOLLLower[len(data.BOLLLower)-1])))
		}
		sb.WriteString(strings.Join(parts, " | ") + "\n\n")
	}
	return sb.String()
}

// formatKlineDataZH 格式化K线数据（中文）
func formatKlineDataZH(symbol string, tfData map[string]*market.TimeframeSeriesData, timeframes []string) string {
	var sb strings.Builder

	for _, tf := range timeframes {
		if data, ok := tfData[tf]; ok && len(data.Klines) > 0 {
			sb.WriteString(fmt.Sprintf("#### %s %s 时间框架 (从旧到新)\n\n", symbol, tf))
			sb.WriteString("```\n")
			sb.WriteString("时间(UTC)      开盘      最高      最低      收盘      成交量\n")

			// 只显示最近30根K线
			startIdx := 0
			if len(data.Klines) > 30 {
				startIdx = len(data.Klines) - 30
			}

			for i := startIdx; i < len(data.Klines); i++ {
				k := data.Klines[i]
				t := time.UnixMilli(k.Time).UTC()
				sb.WriteString(fmt.Sprintf("%s    %s    %s    %s    %s    %.2f\n",
					t.Format("01-02 15:04"),
					formatPriceSmart(k.Open),
					formatPriceSmart(k.High),
					formatPriceSmart(k.Low),
					formatPriceSmart(k.Close),
					k.Volume,
				))
			}

			// 标记最后一根K线
			if len(data.Klines) > 0 {
				sb.WriteString("    <- 当前\n")
			}

			sb.WriteString("```\n\n")

			// 技术指标序列（与K线对齐，从旧到新）
			formatIndicatorSeriesZH(&sb, data)
		}
	}

	return sb.String()
}

// formatIndicatorSeriesZH 输出单个时间框架的技术指标序列（中文）
func formatIndicatorSeriesZH(sb *strings.Builder, data *market.TimeframeSeriesData) {
	wrote := false
	if len(data.EMA20Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA20: %s\n", formatFloatSlice(data.EMA20Values)))
		wrote = true
	}
	if len(data.EMA50Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA50: %s\n", formatFloatSlice(data.EMA50Values)))
		wrote = true
	}
	if len(data.MACDValues) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.MACDValues)))
		wrote = true
	}
	if len(data.RSI7Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI7: %s\n", formatFloatSlice(data.RSI7Values)))
		wrote = true
	}
	if len(data.RSI14Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI14: %s\n", formatFloatSlice(data.RSI14Values)))
		wrote = true
	}
	if data.ATR14 > 0 {
		sb.WriteString(fmt.Sprintf("ATR14: %s\n", formatPriceSmart(data.ATR14)))
		wrote = true
	}
	if len(data.BOLLUpper) > 0 {
		sb.WriteString(fmt.Sprintf("BOLL 上轨: %s\n", formatFloatSlice(data.BOLLUpper)))
		sb.WriteString(fmt.Sprintf("BOLL 中轨: %s\n", formatFloatSlice(data.BOLLMiddle)))
		sb.WriteString(fmt.Sprintf("BOLL 下轨: %s\n", formatFloatSlice(data.BOLLLower)))
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

// formatMarketDataZH 格式化市场数据（中文）
func formatMarketDataZH(strategy_config *store.StrategyConfig, data *market.Data) string {
	if data == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 📈 市场数据概览\n\n")
	sb.WriteString(fmt.Sprintf("### %s 市场数据\n\n", data.Symbol))
	sb.WriteString(fmt.Sprintf("   📈 当前价格: %s\n", formatPriceSmart(data.CurrentPrice)))
	sb.WriteString(fmt.Sprintf(",  📈 当前EMA20: %s\n", formatPriceSmart(data.CurrentEMA20)))
	sb.WriteString(fmt.Sprintf(",  📈 当前MACD: %s\n", formatPriceSmart(data.CurrentMACD)))
	sb.WriteString(fmt.Sprintf(",  📈 当前RSI7: %.3f\n", data.CurrentRSI7))
	sb.WriteString("\n\n")
	if len(data.TimeframeData) > 0 {
		timeframeOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w"}
		for _, tf := range timeframeOrder {
			if tfData, ok := data.TimeframeData[tf]; ok {
				sb.WriteString(fmt.Sprintf("=== %s 时间框架 (从旧到新) ===\n\n", strings.ToUpper(tf)))
				formatTimeframeSeriesDataZH(&sb, tfData)
			}
		}
	} else {
		// 兼容旧数据格式
		if data.IntradaySeries != nil {
			sb.WriteString(fmt.Sprintf("日内序列 (%s 时间间隔，从旧到新):\n\n", strategy_config.Indicators.Klines.PrimaryTimeframe))
			if len(data.IntradaySeries.MidPrices) > 0 {
				sb.WriteString(fmt.Sprintf("中间价: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
			}
			if len(data.IntradaySeries.EMA20Values) > 0 {
				sb.WriteString(fmt.Sprintf("EMA指标 (20期): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
			}
			if len(data.IntradaySeries.MACDValues) > 0 {
				sb.WriteString(fmt.Sprintf("MACD指标: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
			}
			if len(data.IntradaySeries.RSI7Values) > 0 {
				sb.WriteString(fmt.Sprintf("RSI指标 (7期): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
			}
			if len(data.IntradaySeries.RSI14Values) > 0 {
				sb.WriteString(fmt.Sprintf("RSI指标 (14期): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
			}
			if len(data.IntradaySeries.Volume) > 0 {
				sb.WriteString(fmt.Sprintf("成交量: %s\n\n", formatFloatSlice(data.IntradaySeries.Volume)))
			}
			if data.IntradaySeries.ATR14 > 0 {
				sb.WriteString(fmt.Sprintf("3m ATR (14期): %s\n\n", formatPriceSmart(data.IntradaySeries.ATR14)))
			}
		}
		if data.LongerTermContext != nil {
			sb.WriteString("### 📊 更长期市场背景\n\n")
			sb.WriteString(fmt.Sprintf("更长期时间框架 (%s):\n\n", strategy_config.Indicators.Klines.LongerTimeframe))
			sb.WriteString(fmt.Sprintf("EMA20: %s vs. EMA50: %s\n\n", formatPriceSmart(data.LongerTermContext.EMA20), formatPriceSmart(data.LongerTermContext.EMA50)))
			sb.WriteString(fmt.Sprintf("3期ATR: %s vs. 14期ATR: %s\n\n", formatPriceSmart(data.LongerTermContext.ATR3), formatPriceSmart(data.LongerTermContext.ATR14)))
			sb.WriteString(fmt.Sprintf("当前成交量: %.0f vs. 平均成交量: %.0f\n\n", data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))
			if len(data.LongerTermContext.MACDValues) > 0 {
				sb.WriteString((fmt.Sprintf("MACD指标 : %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues))))
			}
			if len(data.LongerTermContext.RSI14Values) > 0 {
				sb.WriteString((fmt.Sprintf("RSI指标 (14期) : %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values))))
			}
		}
	}
	return sb.String()
}

// formatMicrostructureZH 市场微观结构数据格式化（中文）
func formatMicrostructureZH(ms *market.MarketMicrostructure) string {
	if ms == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 📊 市场微观结构分析\n\n")
	sb.WriteString("**说明**: 订单簿分析提供支撑/阻力位、流动性深度和买卖压力信息\n\n")
	sb.WriteString(fmt.Sprintf("### %s 微观结构\n\n", ms.Symbol))

	// Support levels
	if len(ms.SupportLevels) > 0 {
		sb.WriteString("**支撑位** (价格下方的买单聚集区):\n")
		for i, price := range ms.SupportLevels {
			if i >= 3 {
				break // Top 3 only
			}
			distance := (ms.CurrentPrice - price) / ms.CurrentPrice * 100
			sb.WriteString(fmt.Sprintf("- %.2f USDT (距离: -%.2f%%)\n", price, distance))
		}
		sb.WriteString("\n")
	}

	// Resistance levels
	if len(ms.ResistanceLevels) > 0 {
		sb.WriteString("**阻力位** (价格上方的卖单聚集区):\n")
		for i, price := range ms.ResistanceLevels {
			if i >= 3 {
				break // Top 3 only
			}
			distance := (price - ms.CurrentPrice) / ms.CurrentPrice * 100
			sb.WriteString(fmt.Sprintf("- %.2f USDT (距离: +%.2f%%)\n", price, distance))
		}
		sb.WriteString("\n")
	}

	// Order book metrics
	sb.WriteString("**订单簿指标**:\n")
	sb.WriteString(fmt.Sprintf("- 买卖压力: %.2f ", ms.OrderBookImbalance))
	if ms.OrderBookImbalance > 0.6 {
		sb.WriteString("(买盘占优 🟢)\n")
	} else if ms.OrderBookImbalance < 0.4 {
		sb.WriteString("(卖盘占优 🔴)\n")
	} else {
		sb.WriteString("(相对平衡 ⚪)\n")
	}
	sb.WriteString(fmt.Sprintf("- 买卖价差: %.3f%% ", ms.BidAskSpread))
	if ms.BidAskSpread < 0.05 {
		sb.WriteString("(流动性良好)\n")
	} else if ms.BidAskSpread > 0.2 {
		sb.WriteString("(流动性较差)\n")
	} else {
		sb.WriteString("(流动性正常)\n")
	}
	sb.WriteString(fmt.Sprintf("- 订单簿深度: 买%.0f | 卖%.0f USDT\n", ms.BidDepth, ms.AskDepth))
	sb.WriteString(fmt.Sprintf("- VWAP偏离: %.2f%%\n\n", ms.VWAPDeviation))
	sb.WriteString(fmt.Sprintf("- 大宗交易活动: %d 笔大单 (成交量≥ %.2f)\n\n", ms.LargeOrderCount, ms.LargeOrderVolume))

	return sb.String()
}

// formatTimeframeSeriesDataZH 时间框架序列数据格式化（中文）
func formatTimeframeSeriesDataZH(sb *strings.Builder, data *market.TimeframeSeriesData) {
	if len(data.Klines) > 0 {
		sb.WriteString("时间(UTC)      开盘价     最高价     最低价     收盘价     成交量\n")
		for i, k := range data.Klines {
			t := time.Unix(k.Time/1000, 0).UTC()
			timeStr := t.Format("01-02 15:04")
			marker := ""
			if i == len(data.Klines)-1 {
				marker = "  <- 当前"
			}
			sb.WriteString(fmt.Sprintf("%-14s %-9s %-9s %-9s %-9s %-12.2f%s\n",
				timeStr, formatPriceSmart(k.Open), formatPriceSmart(k.High), formatPriceSmart(k.Low), formatPriceSmart(k.Close), k.Volume, marker))
		}
		sb.WriteString("\n")
	} else if len(data.MidPrices) > 0 {
		sb.WriteString(fmt.Sprintf("中间价: %s\n\n", formatFloatSlice(data.MidPrices)))
		if len(data.Volume) > 0 {
			sb.WriteString(fmt.Sprintf("成交量: %s\n\n", formatFloatSlice(data.Volume)))
		}
	}
	if len(data.EMA20Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA20: %s\n", formatFloatSlice(data.EMA20Values)))
	}
	if len(data.EMA50Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA50: %s\n", formatFloatSlice(data.EMA50Values)))
	}
	if len(data.MACDValues) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.MACDValues)))
	}
	if len(data.RSI7Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI7: %s\n", formatFloatSlice(data.RSI7Values)))
	}
	if len(data.RSI14Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI14: %s\n", formatFloatSlice(data.RSI14Values)))
	}
	if data.ATR14 > 0 {
		sb.WriteString(fmt.Sprintf("ATR14: %s\n", formatPriceSmart(data.ATR14)))
	}
	if len(data.BOLLUpper) > 0 {
		sb.WriteString(fmt.Sprintf("BOLL 上轨: %s\n", formatFloatSlice(data.BOLLUpper)))
		sb.WriteString(fmt.Sprintf("BOLL 中轨: %s\n", formatFloatSlice(data.BOLLMiddle)))
		sb.WriteString(fmt.Sprintf("BOLL 下轨: %s\n", formatFloatSlice(data.BOLLLower)))
	}
	sb.WriteString("\n")
}

// formatQuantDataZH 格式化量化数据（中文）
func formatQuantDataZH(data *QuantData) string {
	if data == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 %s 量化数据:\n", data.Symbol))

	if len(data.PriceChange) > 0 {
		sb.WriteString("价格变动: ")
		timeframes := []string{"5m", "15m", "1h", "4h", "12h", "24h"}
		parts := []string{}
		for _, tf := range timeframes {
			if v, ok := data.PriceChange[tf]; ok {
				parts = append(parts, fmt.Sprintf("%s: %+.3f%%", tf, v*100))
			}
		}
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteString("\n")
	}

	if data.Netflow != nil {
		sb.WriteString("资金流向 (Netflow):\n")
		timeframes := []string{"5m", "15m", "1h", "4h", "12h", "24h"}

		if data.Netflow.Institution != nil {
			if len(data.Netflow.Institution.Future) > 0 {
				sb.WriteString("  机构期货:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Institution.Future[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
			if len(data.Netflow.Institution.Spot) > 0 {
				sb.WriteString("  机构现货:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Institution.Spot[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
		}

		if data.Netflow.Personal != nil {
			if len(data.Netflow.Personal.Future) > 0 {
				sb.WriteString("  散户期货:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Personal.Future[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
			if len(data.Netflow.Personal.Spot) > 0 {
				sb.WriteString("  散户现货:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Personal.Spot[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
		}
	}

	return sb.String()
}

// ========== 英文格式化函数 ==========

// formatAccountEN 格式化账户信息（英文）
func formatAccountEN(ctx *Context) string {
	acc := ctx.Account
	var sb strings.Builder

	sb.WriteString("## Account Status\n\n")
	sb.WriteString(fmt.Sprintf("Total Equity: %.2f USDT | ", acc.TotalEquity))
	sb.WriteString(fmt.Sprintf("Available Balance: %.2f USDT (%.1f%%) | ", acc.AvailableBalance, (acc.AvailableBalance/acc.TotalEquity)*100))
	sb.WriteString(fmt.Sprintf("Total PnL: %+.2f%% | ", acc.TotalPnLPct))
	sb.WriteString(fmt.Sprintf("Margin Usage: %.1f%% | ", acc.MarginUsedPct))
	sb.WriteString(fmt.Sprintf("Positions: %d\n\n", acc.PositionCount))

	// Risk warning
	if acc.MarginUsedPct > 85 {
		sb.WriteString("⚠️ **Risk Alert**: Margin usage > 85%, high risk!\n\n")
	} else if acc.MarginUsedPct > 50 {
		sb.WriteString("⚠️ **Risk Notice**: Margin usage > 50%, be cautious with new positions\n\n")
	}

	return sb.String()
}

// formatRecentTradesEN 格式化最近交易（英文）
func formatRecentTradesEN(orders []RecentOrder) string {
	var sb strings.Builder
	sb.WriteString("## Recent Completed Trades\n\n")

	for i, order := range orders {
		profitOrLoss := "Profit"
		if order.RealizedPnL < 0 {
			profitOrLoss = "Loss"
		}

		sb.WriteString(fmt.Sprintf("%d. %s %s | Entry %s Exit %s | %s: %+.2f USDT (%+.2f%%) | %s → %s (%s)\n",
			i+1,
			order.Symbol,
			order.Side,
			formatPriceSmart(order.EntryPrice),
			formatPriceSmart(order.ExitPrice),
			profitOrLoss,
			order.RealizedPnL,
			order.PnLPct,
			order.EntryTime,
			order.ExitTime,
			order.HoldDuration,
		))
	}

	sb.WriteString("\n")
	return sb.String()
}

// formatCurrentPositionsEN 格式化当前持仓（英文）
func formatCurrentPositionsEN(strategy_config *store.StrategyConfig, ctx *Context) string {
	var sb strings.Builder
	sb.WriteString("## Current Positions\n\n")

	for i, pos := range ctx.Positions {
		drawdown := pos.UnrealizedPnLPct - pos.PeakPnLPct

		sb.WriteString(fmt.Sprintf("%d. %s %s | ", i+1, pos.Symbol, strings.ToUpper(pos.Side)))
		sb.WriteString(fmt.Sprintf("Entry %s Current %s | ", formatPriceSmart(pos.EntryPrice), formatPriceSmart(pos.MarkPrice)))
		sb.WriteString(fmt.Sprintf("Qty %.3f | ", pos.Quantity))
		sb.WriteString(fmt.Sprintf("Value %.2f USDT | ", pos.Quantity*pos.MarkPrice))
		sb.WriteString(fmt.Sprintf("PnL %+.2f%% | ", pos.UnrealizedPnLPct))
		sb.WriteString(fmt.Sprintf("PnL Amount %+.2f USDT | ", pos.UnrealizedPnL))
		sb.WriteString(fmt.Sprintf("Peak PnL %.2f%% | ", pos.PeakPnLPct))
		sb.WriteString(fmt.Sprintf("Leverage %dx | ", pos.Leverage))
		sb.WriteString(fmt.Sprintf("Margin %.0f USDT | ", pos.MarginUsed))
		sb.WriteString(fmt.Sprintf("Liq Price %s\n", formatPriceSmart(pos.LiquidationPrice)))

		// Analysis hints
		if drawdown < -0.30*pos.PeakPnLPct && pos.PeakPnLPct > 0.02 {
			sb.WriteString(fmt.Sprintf("   ⚠️ **Take Profit Alert**: PnL dropped from peak %.2f%% to %.2f%%, drawdown %.2f%%, consider taking profit\n",
				pos.PeakPnLPct, pos.UnrealizedPnLPct, (drawdown/pos.PeakPnLPct)*100))
		}

		if pos.UnrealizedPnLPct < -4.0 {
			sb.WriteString("   ⚠️ **Stop Loss Alert**: Loss approaching -5% threshold, consider cutting loss\n")
		}

		if ctx.MarketDataMap != nil {
			if mdata, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(formatMarketDataEN(strategy_config, mdata))
			}
		}
		if ctx.MicrostructureDataMap != nil {
			if ms, ok := ctx.MicrostructureDataMap[pos.Symbol]; ok {
				sb.WriteString(formatMicrostructureEN(ms))
			}
		}
		if ctx.QuantDataMap != nil {
			if qdata, ok := ctx.QuantDataMap[pos.Symbol]; ok {
				sb.WriteString(formatQuantDataEN(qdata))
			}
		}
	}

	return sb.String()
}

// formatCandidateCoinsEN 格式化候选币种（英文）
func formatCandidateCoinsEN(ctx *Context) string {
	var sb strings.Builder
	sb.WriteString("## Candidate Coins\n\n")

	for i, coin := range ctx.CandidateCoins {
		sb.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, coin.Symbol))

		if ctx.MarketDataMap != nil {
			if mdata, ok := ctx.MarketDataMap[coin.Symbol]; ok {
				sb.WriteString(fmt.Sprintf("Current Price: %s\n\n", formatPriceSmart(mdata.CurrentPrice)))

				if mdata.TimeframeData != nil {
					if ctx.ImageChartMode {
						sb.WriteString(formatIndicatorSnapshotEN(coin.Symbol, mdata.TimeframeData, ctx.Timeframes))
					} else {
						sb.WriteString(formatKlineDataEN(coin.Symbol, mdata.TimeframeData, ctx.Timeframes))
					}
				}
			}
		}

		if ctx.QuantDataMap != nil {
			if qdata, ok := ctx.QuantDataMap[coin.Symbol]; ok {
				sb.WriteString(formatQuantDataEN(qdata))
			}
		}
	}

	return sb.String()
}

// formatKlineDataEN 格式化K线数据（英文）
func formatKlineDataEN(symbol string, tfData map[string]*market.TimeframeSeriesData, timeframes []string) string {
	var sb strings.Builder

	// Sort timeframes for consistent output
	sortedTF := make([]string, len(timeframes))
	copy(sortedTF, timeframes)
	sort.Strings(sortedTF)

	for _, tf := range sortedTF {
		if data, ok := tfData[tf]; ok && len(data.Klines) > 0 {
			sb.WriteString(fmt.Sprintf("#### %s %s Timeframe (oldest → latest)\n\n", symbol, tf))
			sb.WriteString("```\n")
			sb.WriteString("Time(UTC)      Open      High      Low       Close     Volume\n")

			startIdx := 0
			if len(data.Klines) > 30 {
				startIdx = len(data.Klines) - 30
			}

			for i := startIdx; i < len(data.Klines); i++ {
				k := data.Klines[i]
				t := time.UnixMilli(k.Time).UTC()
				sb.WriteString(fmt.Sprintf("%s    %s    %s    %s    %s    %.2f\n",
					t.Format("01-02 15:04"),
					formatPriceSmart(k.Open),
					formatPriceSmart(k.High),
					formatPriceSmart(k.Low),
					formatPriceSmart(k.Close),
					k.Volume,
				))
			}

			if len(data.Klines) > 0 {
				sb.WriteString("    <- current\n")
			}

			sb.WriteString("```\n\n")

			// Indicator series (aligned with klines, oldest → latest)
			formatIndicatorSeriesEN(&sb, data)
		}
	}

	return sb.String()
}

// formatIndicatorSeriesEN outputs indicator series for a single timeframe (English)
func formatIndicatorSeriesEN(sb *strings.Builder, data *market.TimeframeSeriesData) {
	wrote := false
	if len(data.EMA20Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA20: %s\n", formatFloatSlice(data.EMA20Values)))
		wrote = true
	}
	if len(data.EMA50Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA50: %s\n", formatFloatSlice(data.EMA50Values)))
		wrote = true
	}
	if len(data.MACDValues) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.MACDValues)))
		wrote = true
	}
	if len(data.RSI7Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI7: %s\n", formatFloatSlice(data.RSI7Values)))
		wrote = true
	}
	if len(data.RSI14Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI14: %s\n", formatFloatSlice(data.RSI14Values)))
		wrote = true
	}
	if data.ATR14 > 0 {
		sb.WriteString(fmt.Sprintf("ATR14: %s\n", formatPriceSmart(data.ATR14)))
		wrote = true
	}
	if len(data.BOLLUpper) > 0 {
		sb.WriteString(fmt.Sprintf("BOLL Upper: %s\n", formatFloatSlice(data.BOLLUpper)))
		sb.WriteString(fmt.Sprintf("BOLL Middle: %s\n", formatFloatSlice(data.BOLLMiddle)))
		sb.WriteString(fmt.Sprintf("BOLL Lower: %s\n", formatFloatSlice(data.BOLLLower)))
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

// formatOptimizedWeightsZH formats optimized trading parameters (Chinese)
func formatOptimizedWeightsZH(weights interface{}) string {
	if weights == nil {
		return ""
	}

	if formatter, ok := weights.(interface{ FormatWeightsForPrompt(lang string) string }); ok {
		return formatter.FormatWeightsForPrompt("zh")
	}

	return ""
}

// formatOptimizedWeightsEN formats optimized trading parameters (English)
func formatOptimizedWeightsEN(weights interface{}) string {
	if weights == nil {
		return ""
	}

	if formatter, ok := weights.(interface{ FormatWeightsForPrompt(lang string) string }); ok {
		return formatter.FormatWeightsForPrompt("en")
	}

	return ""
}

// formatMarketDataEN formats market data (English)
func formatMarketDataEN(strategy_config *store.StrategyConfig, data *market.Data) string {
	if data == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 📈 Market Data Overview\n\n")
	sb.WriteString(fmt.Sprintf("### %s Market Data\n\n", data.Symbol))
	sb.WriteString(fmt.Sprintf("current_price = %s", formatPriceSmart(data.CurrentPrice)))
	sb.WriteString(fmt.Sprintf(", current_ema20 = %s", formatPriceSmart(data.CurrentEMA20)))
	sb.WriteString(fmt.Sprintf(", current_macd = %s", formatPriceSmart(data.CurrentMACD)))
	sb.WriteString(fmt.Sprintf(", current_rsi7 = %.3f", data.CurrentRSI7))
	sb.WriteString("\n\n")
	if len(data.TimeframeData) > 0 {
		timeframeOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w"}
		for _, tf := range timeframeOrder {
			if tfData, ok := data.TimeframeData[tf]; ok {
				sb.WriteString(fmt.Sprintf("=== %s Timeframe (oldest → latest) ===\n\n", strings.ToUpper(tf)))
				formatTimeframeSeriesDataEN(&sb, tfData)
			}
		}
	} else {
		// Compatible with old data format
		if data.IntradaySeries != nil {
			sb.WriteString(fmt.Sprintf("Intraday series (%s intervals, oldest → latest):\n\n", strategy_config.Indicators.Klines.PrimaryTimeframe))
			if len(data.IntradaySeries.MidPrices) > 0 {
				sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
			}
			if len(data.IntradaySeries.EMA20Values) > 0 {
				sb.WriteString(fmt.Sprintf("EMA indicators (20-period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
			}
			if len(data.IntradaySeries.MACDValues) > 0 {
				sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
			}

			if len(data.IntradaySeries.RSI7Values) > 0 {
				sb.WriteString(fmt.Sprintf("RSI indicators (7-Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
			}
			if len(data.IntradaySeries.RSI14Values) > 0 {
				sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
			}
			if len(data.IntradaySeries.Volume) > 0 {
				sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.IntradaySeries.Volume)))
			}
			if data.IntradaySeries.ATR14 > 0 {
				sb.WriteString(fmt.Sprintf("3m ATR (14-period): %s\n\n", formatPriceSmart(data.IntradaySeries.ATR14)))
			}
		}
		if data.LongerTermContext != nil {
			sb.WriteString(fmt.Sprintf("Longer-term context (%s timeframe):\n\n", strategy_config.Indicators.Klines.LongerTimeframe))
			sb.WriteString(fmt.Sprintf("20-Period EMA: %s vs. 50-Period EMA: %s\n\n",
				formatPriceSmart(data.LongerTermContext.EMA20), formatPriceSmart(data.LongerTermContext.EMA50)))
			sb.WriteString(fmt.Sprintf("3-Period ATR: %s vs. 14-Period ATR: %s\n\n",
				formatPriceSmart(data.LongerTermContext.ATR3), formatPriceSmart(data.LongerTermContext.ATR14)))

			sb.WriteString(fmt.Sprintf("Current Volume: %.0f vs. Average Volume: %.0f\n\n",
				data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))
			if len(data.LongerTermContext.MACDValues) > 0 {
				sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
			}
			if len(data.LongerTermContext.RSI14Values) > 0 {
				sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
			}
		}
	}
	return sb.String()
}

// formatMicrostructureEN formats market microstructure data (English)
func formatMicrostructureEN(ms *market.MarketMicrostructure) string {
	if ms == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 📊 Market Microstructure Analysis\n\n")
	sb.WriteString("**Note**: Order book analysis provides support/resistance levels, liquidity depth, and buy/sell pressure\n\n")
	sb.WriteString(fmt.Sprintf("### %s Microstructure\n\n", ms.Symbol))

	// Support levels
	if len(ms.SupportLevels) > 0 {
		sb.WriteString("**Support Levels** (bid order clusters below price):\n")
		for i, price := range ms.SupportLevels {
			if i >= 3 {
				break // Top 3 only
			}
			distance := (ms.CurrentPrice - price) / ms.CurrentPrice * 100
			sb.WriteString(fmt.Sprintf("- $%.2f (distance: -%.2f%%)\n", price, distance))
		}
		sb.WriteString("\n")
	}

	// Resistance levels
	if len(ms.ResistanceLevels) > 0 {
		sb.WriteString("**Resistance Levels** (ask order clusters above price):\n")
		for i, price := range ms.ResistanceLevels {
			if i >= 3 {
				break // Top 3 only
			}
			distance := (price - ms.CurrentPrice) / ms.CurrentPrice * 100
			sb.WriteString(fmt.Sprintf("- $%.2f (distance: +%.2f%%)\n", price, distance))
		}
		sb.WriteString("\n")
	}

	// Order book metrics
	sb.WriteString("**Order Book Metrics**:\n")
	sb.WriteString(fmt.Sprintf("- Order Book Imbalance: %.2f ", ms.OrderBookImbalance))
	if ms.OrderBookImbalance > 0.6 {
		sb.WriteString("(buy pressure 🟢)\n")
	} else if ms.OrderBookImbalance < 0.4 {
		sb.WriteString("(sell pressure 🔴)\n")
	} else {
		sb.WriteString("(balanced ⚪)\n")
	}
	sb.WriteString(fmt.Sprintf("- Spread: %.3f%% ", ms.BidAskSpread))
	if ms.BidAskSpread < 0.05 {
		sb.WriteString("(good liquidity)\n")
	} else if ms.BidAskSpread > 0.2 {
		sb.WriteString("(poor liquidity)\n")
	} else {
		sb.WriteString("(normal liquidity)\n")
	}
	sb.WriteString(fmt.Sprintf("- Order Book Depth: Bid $%.0f | Ask $%.0f\n", ms.BidDepth, ms.AskDepth))
	sb.WriteString(fmt.Sprintf("- VWAP Deviation: %.2f%%\n\n", ms.VWAPDeviation))
	sb.WriteString(fmt.Sprintf("- Large Trade Activity: %d large trades (volume ≥ $%.2f)\n\n", ms.LargeOrderCount, ms.LargeOrderVolume))
	return sb.String()
}

// formatTimeframeSeriesDataEN formats timeframe series data (English)
func formatTimeframeSeriesDataEN(sb *strings.Builder, data *market.TimeframeSeriesData) {
	if len(data.Klines) > 0 {
		sb.WriteString("Time(UTC)      Open      High      Low       Close     Volume\n")
		for i, k := range data.Klines {
			t := time.Unix(k.Time/1000, 0).UTC()
			timeStr := t.Format("01-02 15:04")
			marker := ""
			if i == len(data.Klines)-1 {
				marker = "  <- current"
			}
			sb.WriteString(fmt.Sprintf("%-14s %-9s %-9s %-9s %-9s %-12.2f%s\n",
				timeStr, formatPriceSmart(k.Open), formatPriceSmart(k.High), formatPriceSmart(k.Low), formatPriceSmart(k.Close), k.Volume, marker))
		}
		sb.WriteString("\n")
	} else if len(data.MidPrices) > 0 {
		sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.MidPrices)))
		if len(data.Volume) > 0 {
			sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.Volume)))
		}
	}
	if len(data.EMA20Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA20: %s\n", formatFloatSlice(data.EMA20Values)))
	}
	if len(data.EMA50Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA50: %s\n", formatFloatSlice(data.EMA50Values)))
	}
	if len(data.MACDValues) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.MACDValues)))
	}
	if len(data.RSI7Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI7: %s\n", formatFloatSlice(data.RSI7Values)))
	}
	if len(data.RSI14Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI14: %s\n", formatFloatSlice(data.RSI14Values)))
	}
	if data.ATR14 > 0 {
		sb.WriteString(fmt.Sprintf("ATR14: %s\n", formatPriceSmart(data.ATR14)))
	}
	if len(data.BOLLUpper) > 0 {
		sb.WriteString(fmt.Sprintf("BOLL Upper: %s\n", formatFloatSlice(data.BOLLUpper)))
		sb.WriteString(fmt.Sprintf("BOLL Middle: %s\n", formatFloatSlice(data.BOLLMiddle)))
		sb.WriteString(fmt.Sprintf("BOLL Lower: %s\n", formatFloatSlice(data.BOLLLower)))
	}
	sb.WriteString("\n")
}

// formatQuantDataEN 格式化量化数据（英文）
func formatQuantDataEN(data *QuantData) string {
	if data == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 %s Quantitative Data:\n", data.Symbol))

	if len(data.PriceChange) > 0 {
		sb.WriteString("Price Change: ")
		timeframes := []string{"5m", "15m", "1h", "4h", "12h", "24h"}
		parts := []string{}
		for _, tf := range timeframes {
			if v, ok := data.PriceChange[tf]; ok {
				parts = append(parts, fmt.Sprintf("%s: %+.3f%%", tf, v*100))
			}
		}
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteString("\n")
	}

	if data.Netflow != nil {
		sb.WriteString("Fund Flow (Netflow):\n")
		timeframes := []string{"5m", "15m", "1h", "4h", "12h", "24h"}

		if data.Netflow.Institution != nil {
			if len(data.Netflow.Institution.Future) > 0 {
				sb.WriteString("  Institutional Futures:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Institution.Future[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
			if len(data.Netflow.Institution.Spot) > 0 {
				sb.WriteString("  Institutional Spot:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Institution.Spot[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
		}

		if data.Netflow.Personal != nil {
			if len(data.Netflow.Personal.Future) > 0 {
				sb.WriteString("  Retail Futures:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Personal.Future[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
			if len(data.Netflow.Personal.Spot) > 0 {
				sb.WriteString("  Retail Spot:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Personal.Spot[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
		}
	}

	return sb.String()
}

// =============================================
//  辅助格式化函数
// =============================================

// formatFlowValue 格式化资金流向数值，带单位和符号
func formatFlowValue(v float64) string {
	sign := ""
	if v >= 0 {
		sign = "+"
	}
	absV := v
	if absV < 0 {
		absV = -absV
	}
	if absV >= 1e9 {
		return fmt.Sprintf("%s%.2fB", sign, v/1e9)
	} else if absV >= 1e6 {
		return fmt.Sprintf("%s%.2fM", sign, v/1e6)
	} else if absV >= 1e3 {
		return fmt.Sprintf("%s%.2fK", sign, v/1e3)
	}
	return fmt.Sprintf("%s%.2f", sign, v)
}

// formatFloatSlice 格式化浮点数切片为字符串
func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = formatPriceSmart(v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}
