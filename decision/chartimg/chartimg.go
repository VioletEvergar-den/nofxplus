// Package chartimg 将 K 线序列渲染成 PNG 图片，供多模态 AI 模型以视觉方式读取行情。
//
// 设计原则：
//  1. 纯标准库实现（image/png + 内置 5x7 ASCII 位图字体），零外部依赖，保证 NAS 构建稳定
//  2. 坐标映射为纯函数，渲染后自校验（round-trip），失败即报错，调用方回退文字模式
//  3. 图给形态，图例给精确数值（防模型读错像素）：图例行直接写出 EMA/BOLL 最新值
//  4. 红涨绿跌（中文语境先验），图例显式标注 UP=RED DN=GRN 防止歧义
package chartimg

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"time"
)

// Candle 单根 K 线（与 market.KlineBar 解耦，保持包独立可测试）
type Candle struct {
	Time                   int64 // Unix 秒
	Open, High, Low, Close float64
	Volume                 float64
}

// PriceLevel 水平标注线（如当前价、SL/TP、支撑阻力）
type PriceLevel struct {
	Price float64
	Label string // ASCII 标签，如 "SL" "TP" "PX"
	RGB   uint32 // 0xRRGGBB
}

// Series 渲染输入（指标序列与 Candles 等长对齐，允许为 nil）
type Series struct {
	Symbol    string // 如 DOGEUSDT
	Timeframe string // 如 15m
	Candles   []Candle

	EMA20, EMA50                     []float64
	BOLLUpper, BOLLMiddle, BOLLLower []float64
	MACD                             []float64 // MACD 副图线

	LastPrice float64      // >0 时画当前价虚线，<=0 用最后一根收盘价
	Levels    []PriceLevel // 附加水平线
	DataTime  time.Time    // 数据截至时间（UTC）
}

// 布局常量
const (
	imgW       = 1040
	imgH       = 720
	axisW      = 120 // 右侧价格轴宽度
	plotPad    = 8   // 左右绘图边距
	maxBars    = 100 // 最多渲染 100 根，超出取最近
	charW      = 6   // 5x7 字符宽 + 1px 间距
	charH      = 8   // 7px 高 + 1px
	titleScale = 2
	titleY     = 6
	legendY    = 34
	mainTop    = 56
	mainBot    = 470
	volTop     = 482
	volBot     = 574
	macdTop    = 586
	macdBot    = 678
	timeAxisY  = 686
)

// 配色（币安深色系）
var (
	colBG      = color.RGBA{0x13, 0x17, 0x22, 0xff}
	colGrid    = color.RGBA{0x1e, 0x24, 0x30, 0xff}
	colSep     = color.RGBA{0x2b, 0x31, 0x39, 0xff}
	colText    = color.RGBA{0xd1, 0xd5, 0xdb, 0xff}
	colTextDim = color.RGBA{0x8a, 0x93, 0xa2, 0xff}
	colUp      = color.RGBA{0xf6, 0x46, 0x5d, 0xff} // 红涨
	colDown    = color.RGBA{0x2e, 0xbd, 0x85, 0xff} // 绿跌
	colEMA20   = color.RGBA{0xf0, 0xb9, 0x0b, 0xff} // 琥珀
	colEMA50   = color.RGBA{0xa7, 0x8b, 0xfa, 0xff} // 紫
	colBOLL    = color.RGBA{0x5b, 0x64, 0x72, 0xff}
	colMACD    = color.RGBA{0x4e, 0xcd, 0xc4, 0xff}
	colWhite   = color.RGBA{0xf8, 0xf8, 0xf8, 0xff}
)

// Render 渲染 K 线图为 PNG 字节。任何内部自校验失败都返回 error，调用方应回退文字模式。
func Render(s Series) ([]byte, error) {
	candles := s.Candles
	if len(candles) == 0 {
		return nil, errors.New("chartimg: no candles")
	}
	if len(candles) > maxBars {
		off := len(candles) - maxBars
		candles = candles[off:]
		// 指标序列同步截断对齐
		s.EMA20 = tailSlice(s.EMA20, len(candles))
		s.EMA50 = tailSlice(s.EMA50, len(candles))
		s.BOLLUpper = tailSlice(s.BOLLUpper, len(candles))
		s.BOLLMiddle = tailSlice(s.BOLLMiddle, len(candles))
		s.BOLLLower = tailSlice(s.BOLLLower, len(candles))
		s.MACD = tailSlice(s.MACD, len(candles))
	}

	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	fillRect(img, 0, 0, imgW, imgH, colBG)
	d := &drawer{img: img}

	// ---- 计算主图价格范围（包含 K 线、EMA、BOLL、水平线、当前价）----
	pmin, pmax := math.Inf(1), math.Inf(-1)
	for _, c := range candles {
		pmin = math.Min(pmin, c.Low)
		pmax = math.Max(pmax, c.High)
	}
	for _, arr := range [][]float64{s.EMA20, s.EMA50, s.BOLLUpper, s.BOLLMiddle, s.BOLLLower} {
		for _, v := range arr {
			if isFinite(v) && v > 0 {
				pmin = math.Min(pmin, v)
				pmax = math.Max(pmax, v)
			}
		}
	}
	last := candles[len(candles)-1].Close
	if s.LastPrice > 0 && isFinite(s.LastPrice) {
		pmin = math.Min(pmin, s.LastPrice)
		pmax = math.Max(pmax, s.LastPrice)
	}
	for _, lv := range s.Levels {
		if isFinite(lv.Price) && lv.Price > 0 {
			pmin = math.Min(pmin, lv.Price)
			pmax = math.Max(pmax, lv.Price)
		}
	}
	if !isFinite(pmin) || !isFinite(pmax) {
		return nil, errors.New("chartimg: no finite price data")
	}
	if pmax-pmin < 1e-12 {
		// 全部价格相同（退化数据）：人工给 1% 振幅避免除零
		amp := math.Max(math.Abs(pmax)*0.01, 1e-6)
		pmin -= amp
		pmax += amp
	}
	pad := (pmax - pmin) * 0.05
	pmin -= pad
	pmax += pad

	sc := priceScaler{pmin: pmin, pmax: pmax, yTop: mainTop, yBot: mainBot}

	// ---- 渲染前自校验：坐标映射 round-trip ----
	if err := selfCheck(sc); err != nil {
		return nil, err
	}

	// ---- 标题与图例 ----
	dataTime := s.DataTime
	if dataTime.IsZero() {
		dataTime = time.Unix(candles[len(candles)-1].Time, 0).UTC()
	}
	title := fmt.Sprintf("%s %s  BARS=%d  UTC %s",
		sanitizeASCII(s.Symbol), sanitizeASCII(s.Timeframe), len(candles),
		dataTime.UTC().Format("01-02 15:04"))
	d.text(8, titleY, title, titleScale, colText)

	d.text(8, legendY, "UP=RED DN=GRN", 1, colTextDim)
	x := 8 + 14*charW
	if arr := tailSlice(s.EMA20, len(candles)); len(arr) > 0 && isFinite(arr[len(arr)-1]) {
		x = d.text(x, legendY, "EMA20="+fmtPrice(arr[len(arr)-1], sc), 1, colEMA20)
	}
	if arr := tailSlice(s.EMA50, len(candles)); len(arr) > 0 && isFinite(arr[len(arr)-1]) {
		x = d.text(x, legendY, " EMA50="+fmtPrice(arr[len(arr)-1], sc), 1, colEMA50)
	}
	if arr := tailSlice(s.BOLLUpper, len(candles)); len(arr) > 0 && isFinite(arr[len(arr)-1]) {
		x = d.text(x, legendY, " BOLL_U="+fmtPrice(arr[len(arr)-1], sc), 1, colBOLL)
	}
	if arr := tailSlice(s.BOLLMiddle, len(candles)); len(arr) > 0 && isFinite(arr[len(arr)-1]) {
		x = d.text(x, legendY, " BOLL_M="+fmtPrice(arr[len(arr)-1], sc), 1, colBOLL)
	}
	if arr := tailSlice(s.BOLLLower, len(candles)); len(arr) > 0 && isFinite(arr[len(arr)-1]) {
		x = d.text(x, legendY, " BOLL_L="+fmtPrice(arr[len(arr)-1], sc), 1, colBOLL)
	}

	// ---- 主图网格与价格轴 ----
	plotX0, plotX1 := plotPad, imgW-axisW-4
	step := niceStep((pmax - pmin) / 4.5)
	dec := decimalsForStep(step)
	for t := math.Ceil(pmin/step) * step; t <= pmax; t += step {
		y := sc.priceToY(t)
		if y < mainTop || y > mainBot {
			continue
		}
		for xx := plotX0; xx <= plotX1; xx++ {
			img.Set(xx, y, colGrid)
		}
		d.text(plotX1+6, y-charH/2, formatFloat(t, dec), 1, colTextDim)
	}

	// ---- EMA / BOLL 折线（先画线，蜡烛后画压在上面）----
	drawLineSeries(d, candles, s.BOLLLower, plotX0, plotX1, sc, colBOLL)
	drawLineSeries(d, candles, s.BOLLMiddle, plotX0, plotX1, sc, colBOLL)
	drawLineSeries(d, candles, s.BOLLUpper, plotX0, plotX1, sc, colBOLL)
	drawLineSeries(d, candles, s.EMA50, plotX0, plotX1, sc, colEMA50)
	drawLineSeries(d, candles, s.EMA20, plotX0, plotX1, sc, colEMA20)

	// ---- 水平标注线（虚线 + 右轴反色标签）----
	lp := s.LastPrice
	if lp <= 0 || !isFinite(lp) {
		lp = last
	}
	drawLevel(d, sc, PriceLevel{Price: lp, Label: "PX", RGB: 0xf0b90b}, plotX0, plotX1)
	for _, lv := range s.Levels {
		drawLevel(d, sc, lv, plotX0, plotX1)
	}

	// ---- 蜡烛 ----
	n := len(candles)
	slot := float64(plotX1-plotX0) / float64(n)
	bodyW := int(slot * 0.62)
	if bodyW < 2 {
		bodyW = 2
	}
	if bodyW > 13 {
		bodyW = 13
	}
	for i, c := range candles {
		cx := int(float64(plotX0) + (float64(i)+0.5)*slot)
		col := colUp
		if c.Close < c.Open {
			col = colDown
		}
		yh := clamp(sc.priceToY(c.High), mainTop, mainBot)
		yl := clamp(sc.priceToY(c.Low), mainTop, mainBot)
		for yy := yh; yy <= yl; yy++ {
			img.Set(cx, yy, col)
		}
		yo := sc.priceToY(c.Open)
		yc := sc.priceToY(c.Close)
		top, bot := yo, yc
		if top > bot {
			top, bot = bot, top
		}
		top = clamp(top, mainTop, mainBot)
		bot = clamp(bot, mainTop, mainBot)
		for xx := cx - bodyW/2; xx <= cx+bodyW/2; xx++ {
			for yy := top; yy <= bot; yy++ {
				img.Set(xx, yy, col)
			}
		}
	}

	// ---- 成交量副图 ----
	hline(img, 0, imgW, volTop-8, colSep)
	d.text(plotX0+2, volTop-6, "VOL", 1, colTextDim)
	maxVol := 0.0
	for _, c := range candles {
		if isFinite(c.Volume) {
			maxVol = math.Max(maxVol, c.Volume)
		}
	}
	if maxVol > 0 {
		vh := volBot - volTop - 6
		for i, c := range candles {
			if !isFinite(c.Volume) {
				continue
			}
			bh := int(c.Volume / maxVol * float64(vh))
			col := colUp
			if c.Close < c.Open {
				col = colDown
			}
			cx := int(float64(plotX0) + (float64(i)+0.5)*slot)
			for xx := cx - bodyW/2; xx <= cx+bodyW/2; xx++ {
				for yy := volBot; yy > volBot-bh; yy-- {
					img.Set(xx, yy, col)
				}
			}
		}
	}

	// ---- MACD 副图（对称零轴折线）----
	hline(img, 0, imgW, macdTop-8, colSep)
	d.text(plotX0+2, macdTop-6, "MACD", 1, colTextDim)
	macdMax := 0.0
	for _, v := range s.MACD {
		if isFinite(v) {
			macdMax = math.Max(macdMax, math.Abs(v))
		}
	}
	if macdMax > 0 {
		ms := priceScaler{pmin: -macdMax, pmax: macdMax, yTop: macdTop + 2, yBot: macdBot - 2}
		mid := (macdTop + macdBot) / 2
		hline(img, plotX0, plotX1, mid, colSep)
		drawLineSeriesRaw(d, s.MACD, n, plotX0, plotX1, slot, ms, colMACD)
	}

	// ---- 时间轴 ----
	labelEvery := 1
	if n > 6 {
		labelEvery = (n + 5) / 6
	}
	for i := 0; i < n; i += labelEvery {
		cx := int(float64(plotX0) + (float64(i)+0.5)*slot)
		img.Set(cx, mainBot+2, colSep)
		img.Set(cx, volBot+2, colSep)
		t := time.Unix(candles[i].Time, 0).UTC().Format("01-02 15:04")
		tx := cx - len(t)*charW/2
		if tx < plotX0 {
			tx = plotX0
		}
		d.text(tx, timeAxisY, t, 1, colTextDim)
	}

	// ---- 渲染后自校验：最后一根收盘价的落点必须位于主图内 ----
	ly := sc.priceToY(last)
	if ly < mainTop-1 || ly > mainBot+1 {
		return nil, fmt.Errorf("chartimg: self-check failed, last close y=%d out of pane", ly)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("chartimg: encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// priceScaler 价格↔像素映射（纯函数，便于单测 round-trip）
type priceScaler struct {
	pmin, pmax float64
	yTop, yBot int
}

func (sc priceScaler) priceToY(p float64) int {
	if sc.yBot <= sc.yTop {
		return sc.yTop
	}
	frac := (p - sc.pmin) / (sc.pmax - sc.pmin)
	return sc.yBot - int(math.Round(frac*float64(sc.yBot-sc.yTop)))
}

func (sc priceScaler) yToPrice(y int) float64 {
	if sc.yBot <= sc.yTop {
		return sc.pmin
	}
	frac := float64(sc.yBot-y) / float64(sc.yBot-sc.yTop)
	return sc.pmin + frac*(sc.pmax-sc.pmin)
}

func selfCheck(sc priceScaler) error {
	span := sc.pmax - sc.pmin
	// 像素量化必然带来 ±0.5px 舍入误差，容差取 1.5 个像素对应的价格当量
	pxTol := span / float64(sc.yBot-sc.yTop) * 1.5
	for i := 0; i <= 4; i++ {
		p := sc.pmin + span*float64(i)/4
		y := sc.priceToY(p)
		p2 := sc.yToPrice(y)
		if math.Abs(p2-p) > pxTol {
			return fmt.Errorf("chartimg: price round-trip failed: %v -> y=%d -> %v", p, y, p2)
		}
	}
	return nil
}

// drawLevel 水平虚线 + 右轴反色标签
func drawLevel(d *drawer, sc priceScaler, lv PriceLevel, plotX0, plotX1 int) {
	if !isFinite(lv.Price) {
		return
	}
	y := sc.priceToY(lv.Price)
	if y < mainTop-2 || y > mainBot+2 {
		return
	}
	col := color.RGBA{uint8(lv.RGB >> 16), uint8(lv.RGB >> 8), uint8(lv.RGB), 0xff}
	for xx, i := plotX0, 0; xx <= plotX1; xx, i = xx+1, i+1 {
		if (i/6)%2 == 0 {
			d.img.Set(xx, y, col)
		}
	}
	text := sanitizeASCII(lv.Label) + "=" + strconv.FormatFloat(lv.Price, 'f', -1, 64)
	boxX := imgW - axisW + 2
	w := len(text)*charW + 6
	if w > axisW-4 {
		w = axisW - 4
	}
	fillRect(d.img, boxX, y-charH/2, w, charH, col)
	d.textClip(boxX+2, y-charH/2, text, 1, colWhite, boxX+w)
}

// drawLineSeries 主图指标折线：x 按第 i 根 K 线槽位居中，y 由价格映射（跳过非有限值）
func drawLineSeries(d *drawer, candles []Candle, vals []float64, plotX0, plotX1 int, sc priceScaler, col color.RGBA) {
	if len(vals) == 0 {
		return
	}
	slot := float64(plotX1-plotX0) / float64(len(candles))
	drawLineSeriesRaw(d, vals, len(candles), plotX0, plotX1, slot, sc, col)
}

// drawLineSeriesRaw 通用折线：vals[i] 映射到第 i 个槽位，y = sc.priceToY(v)
func drawLineSeriesRaw(d *drawer, vals []float64, total int, plotX0, plotX1 int, slot float64, sc priceScaler, col color.RGBA) {
	if len(vals) == 0 || total <= 0 || slot <= 0 {
		return
	}
	cnt := len(vals)
	if cnt > total {
		cnt = total
	}
	prevX, prevY, has := 0, 0, false
	for i := 0; i < cnt; i++ {
		v := vals[i]
		if !isFinite(v) {
			has = false
			continue
		}
		x := int(float64(plotX0) + (float64(i)+0.5)*slot)
		y := sc.priceToY(v)
		if has {
			bresenham(d.img, prevX, prevY, x, y, col)
		}
		prevX, prevY, has = x, y, true
	}
}

// ---- 基础绘图 ----

func fillRect(img *image.RGBA, x0, y0, w, h int, c color.RGBA) {
	for y := y0; y < y0+h && y < imgH; y++ {
		for x := x0; x < x0+w && x < imgW; x++ {
			img.Set(x, y, c)
		}
	}
}

func hline(img *image.RGBA, x0, x1, y int, c color.RGBA) {
	if y < 0 || y >= imgH {
		return
	}
	for x := x0; x <= x1 && x < imgW; x++ {
		img.Set(x, y, c)
	}
}

func bresenham(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		img.Set(x0, y0, c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func tailSlice(s []float64, n int) []float64 {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ---- 刻度与数值格式 ----

// niceStep 取"好看"的刻度步长（1/2/2.5/5 × 10^n）
func niceStep(x float64) float64 {
	if x <= 0 || !isFinite(x) {
		return 1
	}
	// 最近值语义：在 {1, 2, 2.5, 5}×10^n 中取与 x 最接近者（保证刻度数为 4~6 根）
	e := math.Floor(math.Log10(x))
	best, bestDiff := 1.0, math.Inf(1)
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		s := m * math.Pow(10, e)
		if d := math.Abs(s - x); d < bestDiff {
			best, bestDiff = s, d
		}
	}
	return best
}

func decimalsForStep(step float64) int {
	switch {
	case step >= 1:
		return 2
	case step >= 0.1:
		return 3
	case step >= 0.01:
		return 4
	case step >= 0.001:
		return 5
	default:
		return 6
	}
}

// fmtPrice 图例用价格格式：按刻度精度推导小数位（小币种如 DOGE 0.08123 不会被抹平）
func fmtPrice(v float64, sc priceScaler) string {
	step := niceStep((sc.pmax - sc.pmin) / 4.5)
	return formatFloat(v, decimalsForStep(step))
}

func formatFloat(v float64, dec int) string {
	return strconv.FormatFloat(v, 'f', dec, 64)
}

// sanitizeASCII 将非支持字符替换为 '?'（内置字体仅支持 ASCII 子集）
func sanitizeASCII(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 128 {
			out = append(out, byte(r))
		} else {
			out = append(out, '?')
		}
	}
	return string(out)
}
