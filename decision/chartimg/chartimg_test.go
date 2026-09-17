package chartimg

import (
	"bytes"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func makeUptrendSeries(n int) Series {
	s := Series{Symbol: "DOGEUSDT", Timeframe: "15m"}
	price := 0.08
	base := int64(1789000000)
	for i := 0; i < n; i++ {
		open := price
		if i%7 == 3 {
			// 周期性回调阴线：保证图中同时出现红涨与绿跌蜡烛
			price -= 0.0002
			if price <= 0.001 {
				price = open
			}
		} else {
			price += 0.0002 + float64(i%3)*0.00005
		}
		closeP := price
		high := math.Max(open, closeP) + 0.0003
		low := math.Min(open, closeP) - 0.0003
		s.Candles = append(s.Candles, Candle{
			Time: base + int64(i)*900, Open: open, High: high, Low: low, Close: closeP,
			Volume: 1000 + float64(i%7)*250,
		})
		s.EMA20 = append(s.EMA20, open+0.0001)
		s.EMA50 = append(s.EMA50, open-0.0001)
		s.BOLLUpper = append(s.BOLLUpper, high+0.0005)
		s.BOLLMiddle = append(s.BOLLMiddle, open)
		s.BOLLLower = append(s.BOLLLower, low-0.0005)
		s.MACD = append(s.MACD, 0.0001*float64(i%5-2))
	}
	return s
}

func TestPriceScalerRoundTrip(t *testing.T) {
	sc := priceScaler{pmin: 0.07, pmax: 0.09, yTop: 56, yBot: 470}
	if err := selfCheck(sc); err != nil {
		t.Fatalf("round-trip self check failed: %v", err)
	}
	// 边界语义：yBot 对应 pmin，yTop 对应 pmax
	if got := sc.yToPrice(sc.priceToY(0.07)); math.Abs(got-0.07) > 1e-9 {
		t.Fatalf("pmin round trip: got %v", got)
	}
	if got := sc.yToPrice(sc.priceToY(0.09)); math.Abs(got-0.09) > 1e-9 {
		t.Fatalf("pmax round trip: got %v", got)
	}
}

func TestNiceStep(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0.002, 0.002},
		{0.003, 0.0025},
		{0.006, 0.005},
		{0.009, 0.01},
		{2500, 2500},
		{3000, 2500},
		{6000, 5000},
		{9000, 10000},
	}
	for _, c := range cases {
		if got := niceStep(c.in); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("niceStep(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRenderUptrendPNG(t *testing.T) {
	s := makeUptrendSeries(40)
	s.LastPrice = s.Candles[39].Close
	s.Levels = []PriceLevel{{Price: s.Candles[39].Close * 0.98, Label: "SL", RGB: 0xff0000}}
	data, err := Render(s)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if b := img.Bounds(); b.Dx() != imgW || b.Dy() != imgH {
		t.Fatalf("unexpected size %dx%d", b.Dx(), b.Dy())
	}
	counts := map[color.RGBA]int{}
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			c := img.At(x, y)
			if rgba, ok := c.(color.RGBA); ok {
				counts[rgba]++
			}
		}
	}
	// 必须出现红涨/绿跌/EMA20 琥珀三种关键颜色
	for _, c := range []color.RGBA{colUp, colDown, colEMA20} {
		if counts[c] == 0 {
			t.Fatalf("expected color %v not found in rendered image", c)
		}
	}
}

func TestRenderDegenerateFlat(t *testing.T) {
	s := Series{Symbol: "TESTUSDT", Timeframe: "5m"}
	for i := 0; i < 20; i++ {
		s.Candles = append(s.Candles, Candle{
			Time: int64(1789000000 + i*300), Open: 0.5, High: 0.5, Low: 0.5, Close: 0.5, Volume: 100,
		})
	}
	if _, err := Render(s); err != nil {
		t.Fatalf("flat data should render with padded range, got error: %v", err)
	}
}

func TestRenderClampsToMaxBars(t *testing.T) {
	s := makeUptrendSeries(250) // 超过 maxBars=100
	data, err := Render(s)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty png")
	}
}

func TestRenderFailsOnEmpty(t *testing.T) {
	if _, err := Render(Series{Symbol: "X"}); err == nil {
		t.Fatal("expected error for empty candles")
	}
}
