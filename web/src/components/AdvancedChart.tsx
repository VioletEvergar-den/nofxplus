import { useEffect, useRef, useState } from 'react'
import {
  createChart,
  IChartApi,
  ISeriesApi,
  Time,
  UTCTimestamp,
  CandlestickSeries,
  LineSeries,
  HistogramSeries,
  createSeriesMarkers,
} from 'lightweight-charts'
import { useLanguage } from '../contexts/LanguageContext'
import { httpClient } from '../lib/httpClient'
import {
  calculateSMA,
  calculateEMA,
  calculateBollingerBands,
  type Kline,
} from '../utils/indicators'
import { Settings, BarChart2, Loader2 } from 'lucide-react'

// 订单接口定义
interface OrderMarker {
  time: number
  price: number
  side: 'long' | 'short'
  rawSide: string // 原始 side 字段 (buy/sell from database)
  action: 'open' | 'close'
  pnl?: number
  symbol: string
}

interface AdvancedChartProps {
  symbol: string
  interval?: string
  traderID?: string
  height?: number
  exchange?: string // 交易所类型：binance, bybit, okx, bitget, hyperliquid, aster, lighter
  onSymbolChange?: (symbol: string) => void // 币种切换回调
}

// 指标配置
interface IndicatorConfig {
  id: string
  name: string
  enabled: boolean
  color: string
  params?: any
}

// 获取成交额货币单位
const getQuoteUnit = (_exchange: string): string => {
  return 'USDT' // 加密货币默认 USDT
}

// 获取成交量数量单位
const getBaseUnit = (_exchange: string, symbol: string): string => {
  // 加密货币：从 symbol 提取基础资产
  const base = symbol.replace(/USDT$|USD$|BUSD$/, '')
  return base || '个'
}

// 格式化大数字
const formatVolume = (value: number): string => {
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B'
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M'
  if (value >= 1e3) return (value / 1e3).toFixed(2) + 'K'
  return value.toFixed(2)
}

// 智能价格小数位：>=100 显示 2 位，1~100 显示 4 位，<1 显示 6 位
const formatPrice = (price: number | undefined): string => {
  if (price === undefined || !isFinite(price)) return '—'
  if (price >= 100) return price.toFixed(2)
  if (price >= 1) return price.toFixed(4)
  return price.toFixed(6)
}

// 根据价格量级推导图表价格轴精度（小币种如 DOGE 需要更多小数位，否则被抹平）
const derivePricePrecision = (price: number): number => {
  if (!isFinite(price) || price <= 0) return 2
  if (price >= 100) return 2
  if (price >= 1) return 4
  if (price >= 0.01) return 5
  return 6
}

export function AdvancedChart({
  symbol = 'BTCUSDT',
  interval = '5m',
  traderID,
  height = 550,
  exchange = 'binance', // 默认使用 binance
  onSymbolChange: _onSymbolChange, // Available for future use
}: AdvancedChartProps) {
  void _onSymbolChange // Prevent unused warning
  const { language } = useLanguage()
  const quoteUnit = getQuoteUnit(exchange)
  const baseUnit = getBaseUnit(exchange, symbol)
  const chartContainerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candlestickSeriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const volumeSeriesRef = useRef<ISeriesApi<'Histogram'> | null>(null)
  const indicatorSeriesRef = useRef<Map<string, ISeriesApi<any>>>(new Map())
  const seriesMarkersRef = useRef<any>(null) // Markers primitive for v5
  const currentMarkersDataRef = useRef<any[]>([]) // 存储当前的标记数据
  const klineDataRef = useRef<Map<number, { volume: number; quoteVolume: number }>>(new Map()) // 存储 kline 额外数据
  const lastKlineDataRef = useRef<Kline[]>([]) // 上一次完整 K 线快照，用于增量对比避免每 5 秒全量重绘
  // 手动价格区间（价格轴滚轮垂直缩放）：非 null 时蜡烛序列的 autoscaleInfoProvider 返回该区间
  const manualPriceRangeRef = useRef<{ min: number; max: number } | null>(null)

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showIndicatorPanel, setShowIndicatorPanel] = useState(false)
  const [showOrderMarkers, setShowOrderMarkers] = useState(true) // 订单标记显示开关，默认显示
  const isInitialLoadRef = useRef(true) // 跟踪是否为初始加载
  const [tooltipData, setTooltipData] = useState<any>(null)
  const tooltipRef = useRef<HTMLDivElement>(null)

  // 行情统计数据（当前K线）
  const [marketStats, setMarketStats] = useState<{
    price: number
    priceChange: number
    priceChangePercent: number
    high: number
    low: number
    volume: number      // 数量（BTC/股数）
    quoteVolume: number // 成交额（USDT/USD）
  } | null>(null)

  // 指标配置
  const [indicators, setIndicators] = useState<IndicatorConfig[]>([
    { id: 'volume', name: 'Volume', enabled: true, color: '#3B82F6' },
    { id: 'ma5', name: 'MA5', enabled: false, color: '#FF6B6B', params: { period: 5 } },
    { id: 'ma10', name: 'MA10', enabled: false, color: '#4ECDC4', params: { period: 10 } },
    { id: 'ma20', name: 'MA20', enabled: false, color: '#FFD93D', params: { period: 20 } },
    { id: 'ma60', name: 'MA60', enabled: false, color: '#95E1D3', params: { period: 60 } },
    { id: 'ema12', name: 'EMA12', enabled: false, color: '#A8E6CF', params: { period: 12 } },
    { id: 'ema26', name: 'EMA26', enabled: false, color: '#FFD3B6', params: { period: 26 } },
    { id: 'bb', name: 'Bollinger Bands', enabled: false, color: '#9B59B6' },
  ])

  // 从服务获取K线数据
  const fetchKlineData = async (symbol: string, interval: string) => {
    try {
      const limit = 1500
      const klineUrl = `/api/klines?symbol=${symbol}&interval=${interval}&limit=${limit}&exchange=${exchange}`
      const result = await httpClient.get(klineUrl)

      if (!result.success || !result.data) {
        throw new Error('Failed to fetch kline data')
      }

      // 转换数据格式
      const rawData = result.data.map((candle: any) => ({
        time: Math.floor(candle.openTime / 1000) as UTCTimestamp,
        open: candle.open,
        high: candle.high,
        low: candle.low,
        close: candle.close,
        volume: candle.volume,           // 数量（BTC/股数）
        quoteVolume: candle.quoteVolume, // 成交额（USDT/USD）
      }))

      // 按时间排序并去重（lightweight-charts 要求数据按时间升序且无重复）
      const sortedData = rawData.sort((a: any, b: any) => a.time - b.time)
      const dedupedData = sortedData.filter((item: any, index: number, arr: any[]) =>
        index === 0 || item.time !== arr[index - 1].time
      )

      if (rawData.length !== dedupedData.length) {
        console.warn('[AdvancedChart] Removed', rawData.length - dedupedData.length, 'duplicate klines')
      }

      return dedupedData
    } catch (err) {
      console.error('[AdvancedChart] Error fetching kline:', err)
      throw err
    }
  }

  // 解析时间：支持 Unix 时间戳（数字）或字符串格式
  const parseCustomTime = (time: any): number => {
    if (!time) {
      console.warn('[AdvancedChart] Empty time value')
      return 0
    }

    // 如果已经是数字（Unix 时间戳），直接返回
    if (typeof time === 'number') {
      console.log('[AdvancedChart] ✅ Unix timestamp:', time, '(', new Date(time * 1000).toISOString(), ')')
      return time
    }

    const timeStr = String(time)
    console.log('[AdvancedChart] Parsing time string:', timeStr)

    // 尝试标准ISO格式
    const isoTime = new Date(timeStr).getTime()
    if (!isNaN(isoTime) && isoTime > 0) {
      const timestamp = Math.floor(isoTime / 1000)
      console.log('[AdvancedChart] ✅ Parsed as ISO:', timeStr, '→', timestamp, '(', new Date(timestamp * 1000).toISOString(), ')')
      return timestamp
    }

    // 解析自定义格式 "MM-DD HH:mm UTC" (兼容旧数据)
    const match = timeStr.match(/(\d{2})-(\d{2})\s+(\d{2}):(\d{2})\s+UTC/)
    if (match) {
      const currentYear = new Date().getFullYear()
      const [_, month, day, hour, minute] = match
      const date = new Date(Date.UTC(
        currentYear,
        parseInt(month) - 1,
        parseInt(day),
        parseInt(hour),
        parseInt(minute)
      ))
      const timestamp = Math.floor(date.getTime() / 1000)
      console.log('[AdvancedChart] ✅ Parsed as custom format:', timeStr, '→', timestamp, '(', new Date(timestamp * 1000).toISOString(), ')')
      return timestamp
    }

    console.error('[AdvancedChart] ❌ Failed to parse time:', timeStr)
    return 0
  }

  // 获取订单数据
  const fetchOrders = async (traderID: string, symbol: string): Promise<OrderMarker[]> => {
    try {
      console.log('[AdvancedChart] Fetching orders for trader:', traderID, 'symbol:', symbol)
      // 获取已成交的订单，限制50条避免标记太多重叠
      const result = await httpClient.get(`/api/orders?trader_id=${traderID}&symbol=${symbol}&status=FILLED&limit=50`)

      console.log('[AdvancedChart] Orders API response:', result)

      if (!result.success || !result.data) {
        console.warn('[AdvancedChart] No orders found, result:', result)
        return []
      }

      const orders = result.data
      console.log('[AdvancedChart] Raw orders data:', orders)
      const markers: OrderMarker[] = []

      orders.forEach((order: any) => {
        console.log('[AdvancedChart] Processing order:', order)

        // 处理字段名：支持PascalCase和snake_case
        const filledAt = order.filled_at || order.FilledAt || order.created_at || order.CreatedAt
        const avgPrice = order.avg_fill_price || order.AvgFillPrice || order.price || order.Price
        const orderAction = order.order_action || order.OrderAction
        const side = (order.side || order.Side)?.toLowerCase() // BUY/SELL
        const symbol = order.symbol || order.Symbol

        // 跳过没有成交时间或价格的订单
        if (!filledAt || !avgPrice || avgPrice === 0) {
          console.warn('[AdvancedChart] Skipping order - missing data:', { filledAt, avgPrice })
          return
        }

        const timeSeconds = parseCustomTime(filledAt)
        if (timeSeconds === 0) {
          console.warn('[AdvancedChart] Skipping order - invalid time:', filledAt)
          return
        }

        // 根据 order_action 判断是开仓还是平仓
        let action: 'open' | 'close' = 'open'
        let positionSide: 'long' | 'short' = 'long'

        if (orderAction) {
          if (orderAction.includes('OPEN')) {
            action = 'open'
            positionSide = orderAction.includes('LONG') ? 'long' : 'short'
          } else if (orderAction.includes('CLOSE')) {
            action = 'close'
            positionSide = orderAction.includes('LONG') ? 'long' : 'short'
          }
        } else {
          // 如果没有 order_action，根据 side 判断
          positionSide = side === 'buy' ? 'long' : 'short'
        }

        console.log('[AdvancedChart] Order marker:', {
          time: timeSeconds,
          price: avgPrice,
          side: positionSide,
          rawSide: side,
          action,
          orderAction
        })

        markers.push({
          time: timeSeconds,
          price: avgPrice,
          side: positionSide,
          rawSide: side, // 原始 side 字段 (buy/sell)
          action: action,
          symbol,
        })
      })

      console.log('[AdvancedChart] Final markers:', markers)
      return markers
    } catch (err) {
      console.error('[AdvancedChart] Error fetching orders:', err)
      return []
    }
  }

  // 初始化图表
  useEffect(() => {
    if (!chartContainerRef.current) return

    const chart = createChart(chartContainerRef.current, {
      width: chartContainerRef.current.clientWidth,
      height: height,
      layout: {
        background: { color: '#0B0E11' },
        textColor: '#B7BDC6',
        fontSize: 12,
      },
      grid: {
        vertLines: {
          color: 'rgba(43, 49, 57, 0.2)',
          style: 1,
          visible: true,
        },
        horzLines: {
          color: 'rgba(43, 49, 57, 0.2)',
          style: 1,
          visible: true,
        },
      },
      crosshair: {
        mode: 1,
        vertLine: {
          color: 'rgba(240, 185, 11, 0.5)',
          width: 1,
          style: 2,
          labelBackgroundColor: '#F0B90B',
        },
        horzLine: {
          color: 'rgba(240, 185, 11, 0.5)',
          width: 1,
          style: 2,
          labelBackgroundColor: '#F0B90B',
        },
      },
      rightPriceScale: {
        borderColor: '#2B3139',
        scaleMargins: {
          top: 0.1,
          bottom: 0.08, // 成交量已移至独立副图，主图无需预留底部空间
        },
        borderVisible: true,
        entireTextOnly: false,
      },
      timeScale: {
        borderColor: '#2B3139',
        timeVisible: true,
        secondsVisible: false,
        borderVisible: true,
        rightOffset: 5,
        barSpacing: 8,
      },
      handleScroll: {
        mouseWheel: true,
        pressedMouseMove: true,
        horzTouchDrag: true,
        vertTouchDrag: true,
      },
      handleScale: {
        axisPressedMouseMove: true,
        mouseWheel: true,
        pinch: true,
      },
      localization: {
        timeFormatter: (time: number) => {
          const date = new Date(time * 1000)
          return date.toLocaleString('zh-CN', {
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            hour12: false,
          })
        },
      },
    })

    chartRef.current = chart

    // 创建K线系列（带手动价格区间支持：价格轴滚轮垂直缩放时覆盖自动缩放）
    const candlestickSeries = chart.addSeries(CandlestickSeries, {
      upColor: '#0ECB81',
      downColor: '#F6465D',
      borderUpColor: '#0ECB81',
      borderDownColor: '#F6465D',
      wickUpColor: '#0ECB81',
      wickDownColor: '#F6465D',
      autoscaleInfoProvider: (base: any) => {
        if (manualPriceRangeRef.current) {
          return {
            priceRange: {
              minValue: manualPriceRangeRef.current.min,
              maxValue: manualPriceRangeRef.current.max,
            },
          }
        }
        return base()
      },
    })
    candlestickSeriesRef.current = candlestickSeries as any

    // 创建成交量系列（独立副图 pane 1，与主图用分隔线隔离）
    const volumeSeries = chart.addSeries(HistogramSeries, {
      color: '#26a69a',
      priceFormat: {
        type: 'volume',
      },
      priceScaleId: 'right',
      lastValueVisible: false,
      priceLineVisible: false,
    }, 1)
    volumeSeriesRef.current = volumeSeries as any

    // 主图:副图 = 4:1，v5 自带 pane 分隔线
    try {
      const panes = chart.panes()
      if (panes.length > 1) {
        panes[0].setStretchFactor(4)
        panes[1].setStretchFactor(1)
      }
    } catch { /* 忽略 panes API 兼容性问题 */ }
    try {
      volumeSeries.priceScale().applyOptions({
        scaleMargins: { top: 0.15, bottom: 0 },
      })
    } catch { /* 忽略 */ }

    // 价格轴滚轮垂直缩放：鼠标悬停在右侧价格轴（主图区域）时滚轮缩放价格范围
    const chartEl = chartContainerRef.current
    // 判定鼠标是否位于右侧价格轴（主图区域）
    const isOverPriceAxis = (clientX: number, clientY: number) => {
      if (!chartEl) return false
      const c = chartRef.current
      if (!c) return false
      const rect = chartEl.getBoundingClientRect()
      const x = clientX - rect.left
      const y = clientY - rect.top
      let axisWidth = 60
      try {
        axisWidth = c.priceScale('right').width() || 60
      } catch { /* 默认值兜底 */ }
      if (x < rect.width - axisWidth) return false
      const mainPaneHeight = c.panes()[0]?.getHeight() ?? rect.height
      return y <= mainPaneHeight
    }
    // 立即触发价格轴重算：v5 的 applyOptions 对 autoscaleInfoProvider 变更不会主动重算缩放，
    // 需通过 update 最后一根K线强制图表重新走 autoscale 流程（provider 读取手动区间 ref）
    const triggerAutoscaleRecalc = () => {
      const series = candlestickSeriesRef.current
      const snapshot = lastKlineDataRef.current
      if (series && snapshot.length > 0) {
        try { series.update(snapshot[snapshot.length - 1] as any) } catch { /* 忽略 */ }
      }
    }
    const handlePriceAxisWheel = (e: WheelEvent) => {
      const c = chartRef.current
      const series = candlestickSeriesRef.current
      if (!c || !series || !chartEl) return
      if (!isOverPriceAxis(e.clientX, e.clientY)) return // 不在价格轴区域，走默认缩放
      e.preventDefault()
      e.stopImmediatePropagation()

      const rect = chartEl.getBoundingClientRect()
      const y = e.clientY - rect.top
      const mainPaneHeight = c.panes()[0]?.getHeight() ?? rect.height
      const top = series.coordinateToPrice(0) as any // 主图顶部对应价格（最大值）
      const bottom = series.coordinateToPrice(mainPaneHeight) as any // 主图底部对应价格（最小值）
      if (top == null || bottom == null || !isFinite(top) || !isFinite(bottom) || top <= bottom) return
      const anchor = series.coordinateToPrice(y) as any
      const anchorPrice = anchor != null && isFinite(anchor) ? anchor : (top + bottom) / 2
      // 向上滚 = 拉伸放大（范围缩小），向下滚 = 压缩缩小；deltaMode=1 为行模式需换算成像素
      const dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY
      const factor = dy < 0 ? 0.85 : 1.15
      const newMin = anchorPrice + (bottom - anchorPrice) * factor
      const newMax = anchorPrice + (top - anchorPrice) * factor
      if (!isFinite(newMin) || !isFinite(newMax) || newMax <= newMin) return
      manualPriceRangeRef.current = { min: newMin, max: newMax }
      triggerAutoscaleRecalc()
    }
    // 双击价格轴恢复自动缩放
    const handlePriceAxisDblClick = (e: MouseEvent) => {
      const series = candlestickSeriesRef.current
      if (!series || !chartEl) return
      if (!isOverPriceAxis(e.clientX, e.clientY)) return
      e.preventDefault()
      e.stopImmediatePropagation()
      manualPriceRangeRef.current = null
      triggerAutoscaleRecalc()
    }
    chartEl?.addEventListener('wheel', handlePriceAxisWheel, { capture: true, passive: false })
    chartEl?.addEventListener('dblclick', handlePriceAxisDblClick, { capture: true })

    // 响应式调整
    const handleResize = () => {
      if (chartContainerRef.current && chartRef.current) {
        chartRef.current.applyOptions({
          width: chartContainerRef.current.clientWidth,
        })
      }
    }

    window.addEventListener('resize', handleResize)

    // 监听鼠标移动，显示 OHLC 信息
    chart.subscribeCrosshairMove((param) => {
      if (!param.time || !param.point || !candlestickSeriesRef.current) {
        setTooltipData(null)
        return
      }

      const data = param.seriesData.get(candlestickSeriesRef.current as any)
      if (!data) {
        setTooltipData(null)
        return
      }

      const candleData = data as any

      // 从存储的数据中获取 volume 和 quoteVolume
      const klineExtra = klineDataRef.current.get(param.time as number) || { volume: 0, quoteVolume: 0 }

      setTooltipData({
        time: param.time,
        open: candleData.open,
        high: candleData.high,
        low: candleData.low,
        close: candleData.close,
        volume: klineExtra.volume,
        quoteVolume: klineExtra.quoteVolume,
        x: param.point.x,
        y: param.point.y,
      })
    })

    return () => {
      window.removeEventListener('resize', handleResize)
      chartEl?.removeEventListener('wheel', handlePriceAxisWheel, { capture: true } as any)
      chartEl?.removeEventListener('dblclick', handlePriceAxisDblClick, { capture: true } as any)
      chart.remove()
    }
  }, [height])

  // 加载数据和指标
  useEffect(() => {
    // 当 symbol 或 interval 改变时，重置初始加载标志（以便自动适配新数据）
    isInitialLoadRef.current = true
    lastKlineDataRef.current = []
    // 切换币种/周期时清除价格轴手动缩放区间，恢复自动缩放
    manualPriceRangeRef.current = null

    // 清除旧的标记数据，避免旧数据影响新图表
    currentMarkersDataRef.current = []
    if (seriesMarkersRef.current) {
      try {
        seriesMarkersRef.current.setMarkers([])
      } catch (e) {
        // 忽略错误，稍后会重新创建
      }
      seriesMarkersRef.current = null
    }

    const loadData = async (isRefresh = false) => {
      if (!candlestickSeriesRef.current) return

      // 只在首次加载时显示 loading，刷新时不显示避免闪烁
      if (!isRefresh) {
        setLoading(true)
      }
      setError(null)

      try {
        // 1. 获取K线数据
        const klineData = await fetchKlineData(symbol, interval)

        // ===== 增量更新优化 =====
        // 刷新时与上次快照对比：无变化则跳过、仅尾部变化则只 update 尾部蜡烛，
        // 避免每 5 秒对 1500 根 K 线做全量 setData 触发 canvas 全量重绘（Edge 软渲染下尤为卡顿）
        const prevData = lastKlineDataRef.current
        const prevLen = prevData.length
        const newLen = klineData.length
        let applyMode: 'full' | 'tail' | 'skip' = 'full'
        let tailStart = 0 // klineData 中需要增量 update 的起始下标

        if (isRefresh && prevLen > 0 && newLen > 0) {
          const prevLast = prevData[prevLen - 1]
          const newLast = klineData[newLen - 1]
          if (newLen === prevLen && newLast.time === prevLast.time) {
            // 长度不变：只有最后一根（进行中蜡烛）可能变化
            const tailChanged =
              newLast.open !== prevLast.open ||
              newLast.high !== prevLast.high ||
              newLast.low !== prevLast.low ||
              newLast.close !== prevLast.close ||
              newLast.volume !== prevLast.volume
            applyMode = tailChanged ? 'tail' : 'skip'
            tailStart = newLen - 1
          } else if (
            newLen > prevLen &&
            newLast.time > prevLast.time &&
            klineData[prevLen - 1] &&
            klineData[prevLen - 1].time === prevLast.time
          ) {
            // 追加了新蜡烛且旧数据对齐：从最后一根旧蜡烛（可能已收盘变化）开始增量 update
            applyMode = 'tail'
            tailStart = prevLen - 1
          }
        }

        if (applyMode === 'tail') {
          for (let i = tailStart; i < newLen; i++) {
            candlestickSeriesRef.current.update(klineData[i] as any)
          }
        } else if (applyMode === 'full') {
          // 全量路径：按最新价格量级动态设置价格轴精度（小币种如 DOGE 需 4~6 位小数，避免被抹平）
          const lastClose = klineData[klineData.length - 1]?.close
          if (lastClose !== undefined) {
            const precision = derivePricePrecision(lastClose)
            candlestickSeriesRef.current.applyOptions({
              priceFormat: {
                type: 'price',
                precision,
                minMove: Math.pow(10, -precision),
              },
            } as any)
          }
          candlestickSeriesRef.current.setData(klineData)
        }
        lastKlineDataRef.current = klineData

        // 存储 volume/quoteVolume 数据供 tooltip 使用
        if (applyMode === 'full') {
          klineDataRef.current.clear()
          klineData.forEach((k: any) => {
            klineDataRef.current.set(k.time, { volume: k.volume || 0, quoteVolume: k.quoteVolume || 0 })
          })
        } else if (applyMode === 'tail') {
          for (let i = tailStart; i < newLen; i++) {
            const k: any = klineData[i]
            klineDataRef.current.set(k.time, { volume: k.volume || 0, quoteVolume: k.quoteVolume || 0 })
          }
        }

        // 1.5 计算行情统计数据
        if (klineData.length > 1) {
          const latestKline = klineData[klineData.length - 1]
          const prevKline = klineData[klineData.length - 2]

          // 涨跌幅：当前K线收盘价 vs 前一根K线收盘价
          const priceChange = latestKline.close - prevKline.close
          const priceChangePercent = (priceChange / prevKline.close) * 100

          setMarketStats({
            price: latestKline.close,
            priceChange,
            priceChangePercent,
            high: latestKline.high,
            low: latestKline.low,
            volume: latestKline.volume || 0,
            quoteVolume: latestKline.quoteVolume || 0,
          })
        } else if (klineData.length === 1) {
          const latestKline = klineData[0]
          setMarketStats({
            price: latestKline.close,
            priceChange: 0,
            priceChangePercent: 0,
            high: latestKline.high,
            low: latestKline.low,
            volume: latestKline.volume || 0,
            quoteVolume: latestKline.quoteVolume || 0,
          })
        }

        // 2. 显示成交量（与主图同步走增量路径）
        if (volumeSeriesRef.current) {
          const volumeEnabled = indicators.find(i => i.id === 'volume')?.enabled
          if (volumeEnabled) {
            if (applyMode === 'tail') {
              for (let i = tailStart; i < newLen; i++) {
                const k: any = klineData[i]
                volumeSeriesRef.current.update({
                  time: k.time,
                  value: k.volume || 0,
                  color: k.close >= k.open ? 'rgba(14, 203, 129, 0.5)' : 'rgba(246, 70, 93, 0.5)',
                })
              }
            } else if (applyMode === 'full') {
              const volumeData = klineData.map((k: Kline) => ({
                time: k.time,
                value: k.volume || 0,
                color: k.close >= k.open ? 'rgba(14, 203, 129, 0.5)' : 'rgba(246, 70, 93, 0.5)',
              }))
              volumeSeriesRef.current.setData(volumeData)
            }
            // applyMode === 'skip' 时成交量也无变化，跳过
          } else if (applyMode === 'full') {
            // 关闭成交量时清空数据（仅全量路径需要处理）
            volumeSeriesRef.current.setData([])
          }
        }

        // 3. 添加指标（skip 时跳过；tail 且未启用叠加指标时跳过，避免无谓重建）
        const hasOverlayIndicator = indicators.some(i => i.enabled && i.id !== 'volume')
        if (applyMode === 'full' || (applyMode === 'tail' && hasOverlayIndicator)) {
          updateIndicators(klineData)
        }

        // 4. 获取并显示订单标记（只在首次加载时拉取，避免每 5 秒重复请求与重建标记）
        if (traderID && candlestickSeriesRef.current && !isRefresh) {
          const orders = await fetchOrders(traderID, symbol)

          if (orders.length > 0) {
            // 提取 K 线时间数组（已排序）
            const klineTimes = klineData.map((k: any) => k.time as number)
            const klineMinTime = klineTimes[0] || 0
            const klineMaxTime = klineTimes[klineTimes.length - 1] || 0

            // 二分查找：找到订单时间所属的 K 线蜡烛
            // 返回 time <= orderTime 的最大 K 线时间
            const findCandleTime = (orderTime: number): number | null => {
              if (orderTime < klineMinTime || orderTime > klineMaxTime) {
                return null // 超出范围
              }

              let left = 0
              let right = klineTimes.length - 1

              while (left < right) {
                const mid = Math.ceil((left + right + 1) / 2)
                if (klineTimes[mid] <= orderTime) {
                  left = mid
                } else {
                  right = mid - 1
                }
              }

              return klineTimes[left]
            }

            // 过滤并对齐订单到 K 线时间
            const markers: Array<{
              time: Time
              position: 'belowBar'
              color: string
              shape: 'circle'
              text: string
              size: number
            }> = []

            orders.forEach(order => {
              // 使用二分查找找到对应的 K 线蜡烛时间
              const candleTime = findCandleTime(order.time)

              if (candleTime === null) {
                return
              }

              const isBuy = order.rawSide === 'buy'
              markers.push({
                time: candleTime as Time,
                position: 'belowBar' as const,
                color: isBuy ? '#0ECB81' : '#F6465D',
                shape: 'circle' as const,
                text: isBuy ? 'B' : 'S',
                size: 1,
              })
            })

            // 按时间排序（lightweight-charts 要求标记按时间顺序）
            markers.sort((a, b) => (a.time as number) - (b.time as number))

            try {
              // 存储标记数据供后续切换使用
              currentMarkersDataRef.current = markers

              // 使用 v5 API: createSeriesMarkers
              const markersToShow = showOrderMarkers ? markers : []

              if (seriesMarkersRef.current) {
                // 如果已经存在，更新标记
                seriesMarkersRef.current.setMarkers(markersToShow)
              } else {
                // 首次创建标记
                seriesMarkersRef.current = createSeriesMarkers(candlestickSeriesRef.current, markersToShow)
              }
            } catch (err) {
              console.error('[AdvancedChart] ❌ Failed to set markers:', err)
            }
          } else {
            try {
              if (seriesMarkersRef.current) {
                seriesMarkersRef.current.setMarkers([])
              }
            } catch (err) {
              console.error('[AdvancedChart] Failed to clear markers:', err)
            }
          }
        }

        // 只在初始加载时自动适配视图，避免刷新时抖动
        if (isInitialLoadRef.current) {
          chartRef.current?.timeScale().fitContent()
          isInitialLoadRef.current = false
        }
        setLoading(false)
      } catch (err: any) {
        console.error('[AdvancedChart] Error loading data:', err)
        setError(err.message || 'Failed to load chart data')
        setLoading(false)
      }
    }

    loadData(false) // 首次加载

    // 实时自动刷新 (5秒更新一次)
    const refreshInterval = setInterval(() => loadData(true), 5000)
    return () => clearInterval(refreshInterval)
  }, [symbol, interval, traderID, exchange])

  // 单独处理订单标记的显示/隐藏，避免重新加载数据
  useEffect(() => {
    if (!seriesMarkersRef.current) return

    try {
      const markersToShow = showOrderMarkers ? currentMarkersDataRef.current : []
      seriesMarkersRef.current.setMarkers(markersToShow)
      console.log('[AdvancedChart] 🔄 Toggled markers visibility:', showOrderMarkers, 'Count:', markersToShow.length)
    } catch (err) {
      console.error('[AdvancedChart] ❌ Failed to toggle markers:', err)
    }
  }, [showOrderMarkers])

  // 更新指标
  const updateIndicators = (klineData: Kline[]) => {
    if (!chartRef.current) return

    // 叠加指标 autoscale：手动价格区间激活时不贡献范围，避免把主图拉回自动缩放
    const overlayAutoscale = (base: any) => {
      if (manualPriceRangeRef.current) return null
      return base()
    }

    // 清除旧指标
    indicatorSeriesRef.current.forEach(series => {
      chartRef.current?.removeSeries(series as any)
    })
    indicatorSeriesRef.current.clear()

    // 添加启用的指标
    indicators.forEach(indicator => {
      if (!indicator.enabled || !chartRef.current) return

      if (indicator.id.startsWith('ma')) {
        const maData = calculateSMA(klineData, indicator.params.period)
        const series = chartRef.current.addSeries(LineSeries, {
          color: indicator.color,
          lineWidth: 2,
          title: indicator.name,
          autoscaleInfoProvider: overlayAutoscale,
        } as any)
        series.setData(maData as any)
        indicatorSeriesRef.current.set(indicator.id, series)
      } else if (indicator.id.startsWith('ema')) {
        const emaData = calculateEMA(klineData, indicator.params.period)
        const series = chartRef.current.addSeries(LineSeries, {
          color: indicator.color,
          lineWidth: 2,
          title: indicator.name,
          lineStyle: 2, // 虚线
          autoscaleInfoProvider: overlayAutoscale,
        } as any)
        series.setData(emaData as any)
        indicatorSeriesRef.current.set(indicator.id, series)
      } else if (indicator.id === 'bb') {
        const bbData = calculateBollingerBands(klineData)

        const upperSeries = chartRef.current.addSeries(LineSeries, {
          color: indicator.color,
          lineWidth: 1,
          title: 'BB Upper',
          autoscaleInfoProvider: overlayAutoscale,
        } as any)
        upperSeries.setData(bbData.map(d => ({ time: d.time as any, value: d.upper })))

        const middleSeries = chartRef.current.addSeries(LineSeries, {
          color: indicator.color,
          lineWidth: 1,
          lineStyle: 2,
          title: 'BB Middle',
          autoscaleInfoProvider: overlayAutoscale,
        } as any)
        middleSeries.setData(bbData.map(d => ({ time: d.time as any, value: d.middle })))

        const lowerSeries = chartRef.current.addSeries(LineSeries, {
          color: indicator.color,
          lineWidth: 1,
          title: 'BB Lower',
          autoscaleInfoProvider: overlayAutoscale,
        } as any)
        lowerSeries.setData(bbData.map(d => ({ time: d.time as any, value: d.lower })))

        indicatorSeriesRef.current.set(indicator.id + '_upper', upperSeries)
        indicatorSeriesRef.current.set(indicator.id + '_middle', middleSeries)
        indicatorSeriesRef.current.set(indicator.id + '_lower', lowerSeries)
      }
    })
  }

  // 切换指标
  const toggleIndicator = (id: string) => {
    setIndicators(prev =>
      prev.map(ind => (ind.id === id ? { ...ind, enabled: !ind.enabled } : ind))
    )
  }

  return (
    <div
      className="relative shadow-xl"
      style={{
        background: 'linear-gradient(180deg, #0F1215 0%, #0B0E11 100%)',
        borderRadius: '12px',
        overflow: 'hidden',
        border: '1px solid rgba(43, 49, 57, 0.5)',
      }}
    >
      {/* Compact Professional Header */}
      <div
        className="flex items-center justify-between px-4 py-2"
        style={{ borderBottom: '1px solid rgba(43, 49, 57, 0.6)', background: '#0D1117' }}
      >
        {/* Left: Symbol Info + Price */}
        <div className="flex items-center gap-4">
          {/* Symbol & Interval */}
          <div className="flex items-center gap-2">
            <span className="text-sm font-bold text-white">{symbol}</span>
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-[#1F2937] text-gray-400">{interval}</span>
            <span
              className="text-[10px] px-1.5 py-0.5 rounded font-medium uppercase"
              style={{
                background: exchange === 'hyperliquid' ? 'rgba(80, 227, 194, 0.1)' : 'rgba(243, 186, 47, 0.1)',
                color: exchange === 'hyperliquid' ? '#50E3C2' : '#F3BA2F',
              }}
            >
              {exchange?.toUpperCase()}
            </span>
          </div>

          {/* Price Display */}
          {marketStats && (
            <div className="flex items-center gap-3 pl-3 border-l border-[#2B3139]">
              <span
                className="text-base font-bold tabular-nums"
                style={{ color: marketStats.priceChange >= 0 ? '#10B981' : '#EF4444' }}
              >
                {formatPrice(marketStats.price)}
              </span>
              <span
                className="text-xs font-medium px-1.5 py-0.5 rounded tabular-nums"
                style={{
                  background: marketStats.priceChange >= 0 ? 'rgba(16, 185, 129, 0.1)' : 'rgba(239, 68, 68, 0.1)',
                  color: marketStats.priceChange >= 0 ? '#10B981' : '#EF4444',
                }}
              >
                {marketStats.priceChange >= 0 ? '+' : ''}{marketStats.priceChangePercent.toFixed(2)}%
              </span>

              {/* Compact H/L */}
              <div className="flex items-center gap-2 text-[11px] text-gray-500">
                <span>H <span className="text-gray-300">{formatPrice(marketStats.high)}</span></span>
                <span>L <span className="text-gray-300">{formatPrice(marketStats.low)}</span></span>
                {marketStats.volume > 0 && baseUnit && (
                  <span>Vol <span className="text-gray-300">{formatVolume(marketStats.volume)}</span></span>
                )}
              </div>
            </div>
          )}
        </div>

        {/* Right: Controls */}
        <div className="flex items-center gap-1.5">
          {loading && (
            <span className="text-[10px] text-yellow-400 animate-pulse mr-2">
              {language === 'zh' ? '更新中...' : 'Updating...'}
            </span>
          )}
          <button
            onClick={() => setShowIndicatorPanel(!showIndicatorPanel)}
            className="flex items-center gap-1 px-2 py-1 rounded text-[11px] font-medium transition-all"
            style={{
              background: showIndicatorPanel ? 'rgba(96, 165, 250, 0.15)' : 'transparent',
              color: showIndicatorPanel ? '#60A5FA' : '#6B7280',
            }}
          >
            <Settings className="w-3 h-3" />
            <span>{language === 'zh' ? '指标' : 'Indicators'}</span>
          </button>

          <button
            onClick={() => setShowOrderMarkers(!showOrderMarkers)}
            className="flex items-center gap-1 px-2 py-1 rounded text-[11px] font-medium transition-all"
            style={{
              background: showOrderMarkers ? 'rgba(16, 185, 129, 0.15)' : 'transparent',
              color: showOrderMarkers ? '#10B981' : '#6B7280',
            }}
            title={language === 'zh' ? '订单标记' : 'Order Markers'}
          >
            <span>B/S</span>
          </button>
        </div>
      </div>

      {/* 指标面板 - 专业化设计 */}
      {showIndicatorPanel && (
        <div
          className="absolute top-16 right-4 z-10 rounded-lg shadow-2xl backdrop-blur-sm"
          style={{
            background: 'linear-gradient(135deg, #1A1E23 0%, #0F1215 100%)',
            border: '1px solid rgba(240, 185, 11, 0.2)',
            maxHeight: '500px',
            minWidth: '280px',
            overflowY: 'auto',
          }}
        >
          {/* 标题栏 */}
          <div
            className="flex items-center justify-between px-4 py-3 border-b"
            style={{ borderColor: 'rgba(43, 49, 57, 0.5)' }}
          >
            <div className="flex items-center gap-2">
              <BarChart2 className="w-4 h-4 text-yellow-400" />
              <h4 className="text-sm font-bold text-white">
                {language === 'zh' ? '技术指标' : 'Technical Indicators'}
              </h4>
            </div>
            <button
              onClick={() => setShowIndicatorPanel(false)}
              className="text-gray-400 hover:text-white transition-colors"
            >
              <span className="text-lg">×</span>
            </button>
          </div>

          {/* 指标列表 */}
          <div className="p-3 space-y-1">
            {indicators.map(indicator => (
              <label
                key={indicator.id}
                className="flex items-center gap-3 p-2.5 rounded-md hover:bg-white/5 cursor-pointer transition-all group"
              >
                <div className="relative">
                  <input
                    type="checkbox"
                    checked={indicator.enabled}
                    onChange={() => toggleIndicator(indicator.id)}
                    className="w-4 h-4 rounded border-gray-600 text-yellow-500 focus:ring-2 focus:ring-yellow-500/50"
                  />
                </div>
                <div
                  className="w-8 h-3 rounded-sm border border-white/10"
                  style={{ backgroundColor: indicator.color }}
                ></div>
                <span className="text-sm text-gray-300 group-hover:text-white transition-colors flex-1">
                  {indicator.name}
                </span>
                {indicator.enabled && (
                  <span className="text-xs text-yellow-400">●</span>
                )}
              </label>
            ))}
          </div>

          {/* 底部提示 */}
          <div
            className="px-4 py-2 text-xs text-gray-500 border-t"
            style={{ borderColor: 'rgba(43, 49, 57, 0.5)' }}
          >
            {language === 'zh' ? '点击选择需要显示的指标' : 'Click to toggle indicators'}
          </div>
        </div>
      )}

      {/* 图表容器 */}
      <div style={{ position: 'relative' }}>
        <div ref={chartContainerRef} />

        {/* OHLC Tooltip（跟随十字光标，靠近右缘时自动翻转） */}
        {tooltipData && (() => {
          const containerWidth = chartContainerRef.current?.clientWidth || 0
          const flipX = tooltipData.x > containerWidth * 0.55
          const candleChange =
            tooltipData.open && tooltipData.open !== 0
              ? ((tooltipData.close - tooltipData.open) / tooltipData.open) * 100
              : 0
          const up = candleChange >= 0
          return (
            <div
              ref={tooltipRef}
              style={{
                position: 'absolute',
                left: flipX ? undefined : Math.min(tooltipData.x + 16, Math.max(containerWidth - 180, 8)),
                right: flipX ? Math.max(containerWidth - tooltipData.x + 16, 8) : undefined,
                top: Math.max(8, tooltipData.y - 24),
                padding: '8px 12px',
                background: 'rgba(15, 18, 21, 0.95)',
                border: '1px solid rgba(240, 185, 11, 0.3)',
                borderRadius: '8px',
                color: '#EAECEF',
                fontSize: '12px',
                fontFamily: 'monospace',
                pointerEvents: 'none',
                zIndex: 10,
                backdropFilter: 'blur(10px)',
                boxShadow: '0 4px 12px rgba(0, 0, 0, 0.5)',
              }}
            >
              <div style={{ marginBottom: '6px', color: '#F0B90B', fontWeight: 'bold', fontSize: '11px' }}>
                {new Date((tooltipData.time as number) * 1000).toLocaleString(language === 'zh' ? 'zh-CN' : 'en-US', {
                  month: 'short',
                  day: 'numeric',
                  hour: '2-digit',
                  minute: '2-digit',
                })}
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '4px 12px', fontSize: '11px' }}>
                <span style={{ color: '#848E9C' }}>O:</span>
                <span style={{ color: '#EAECEF', fontWeight: '500' }}>{formatPrice(tooltipData.open)}</span>

                <span style={{ color: '#848E9C' }}>H:</span>
                <span style={{ color: '#0ECB81', fontWeight: '500' }}>{formatPrice(tooltipData.high)}</span>

                <span style={{ color: '#848E9C' }}>L:</span>
                <span style={{ color: '#F6465D', fontWeight: '500' }}>{formatPrice(tooltipData.low)}</span>

                <span style={{ color: '#848E9C' }}>C:</span>
                <span style={{
                  color: tooltipData.close >= tooltipData.open ? '#0ECB81' : '#F6465D',
                  fontWeight: 'bold'
                }}>
                  {formatPrice(tooltipData.close)}
                </span>

                <span style={{ color: '#848E9C' }}>{language === 'zh' ? '涨跌' : 'Chg'}:</span>
                <span style={{ color: up ? '#0ECB81' : '#F6465D', fontWeight: 'bold' }}>
                  {up ? '+' : ''}{candleChange.toFixed(2)}%
                </span>

                {tooltipData.volume > 0 && baseUnit && (
                  <>
                    <span style={{ color: '#848E9C' }}>V({baseUnit}):</span>
                    <span style={{ color: '#3B82F6', fontWeight: '500' }}>
                      {formatVolume(tooltipData.volume)}
                    </span>
                  </>
                )}

                {tooltipData.quoteVolume > 0 && quoteUnit && (
                  <>
                    <span style={{ color: '#848E9C' }}>V({quoteUnit}):</span>
                    <span style={{ color: '#3B82F6', fontWeight: '500' }}>
                      {formatVolume(tooltipData.quoteVolume)}
                    </span>
                  </>
                )}
              </div>
            </div>
          )
        })()}

        {/* 初始加载遮罩 */}
        {loading && !marketStats && !error && (
          <div
            className="absolute inset-0 flex items-center justify-center"
            style={{ background: 'rgba(11, 14, 17, 0.55)', zIndex: 5 }}
          >
            <div className="flex items-center gap-2 text-xs" style={{ color: '#F0B90B' }}>
              <Loader2 className="w-4 h-4 animate-spin" />
              {language === 'zh' ? '加载行情数据...' : 'Loading market data...'}
            </div>
          </div>
        )}

        {/* NOFX 水印 */}
        <div
          style={{
            position: 'absolute',
            bottom: '20%',
            right: '5%',
            pointerEvents: 'none',
            userSelect: 'none',
            zIndex: 1,
          }}
        >
          <div
            style={{
              fontSize: '56px',
              fontWeight: '700',
              color: 'rgba(240, 185, 11, 0.12)',
              letterSpacing: '4px',
              fontFamily: 'system-ui, -apple-system, BlinkMacSystemFont, sans-serif',
              textShadow: '0 2px 30px rgba(240, 185, 11, 0.2)',
            }}
          >
            NOFX
          </div>
        </div>
      </div>

      {/* 错误提示 */}
      {error && (
        <div
          className="absolute inset-0 flex items-center justify-center"
          style={{ background: 'rgba(11, 14, 17, 0.9)' }}
        >
          <div className="text-center">
            <div className="text-2xl mb-2">⚠️</div>
            <div style={{ color: '#F6465D' }}>{error}</div>
          </div>
        </div>
      )}

    </div>
  )
}
