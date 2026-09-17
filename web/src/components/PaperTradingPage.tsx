import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../lib/api'
import type { AccountInfo, DecisionRecord, Position, TraderInfo } from '../types'
import { useLanguage } from '../contexts/LanguageContext'
import { DecisionCard } from './DecisionCard'
import { AdvancedChart } from './AdvancedChart'
import { Loader2, RefreshCw, X } from 'lucide-react'

// 智能价格小数位：>=100 显示 2 位，1~100 显示 4 位，<1 显示 6 位
function fmtPrice(price: number | undefined | null): string {
  if (price === undefined || price === null || !isFinite(price) || price === 0) return '—'
  if (price >= 100) return price.toFixed(2)
  if (price >= 1) return price.toFixed(4)
  return price.toFixed(6)
}

function fmtUsd(v: number | undefined | null): string {
  if (v === undefined || v === null || !isFinite(v)) return '—'
  return v.toFixed(2)
}

const INTERVALS = ['1m', '5m', '15m', '1h', '4h', '1d']
const SYMBOLS = [
  'BTCUSDT', 'ETHUSDT', 'BNBUSDT', 'SOLUSDT', 'XRPUSDT',
  'DOGEUSDT', 'ADAUSDT', 'AVAXUSDT', 'LINKUSDT', 'SUIUSDT',
]

export function PaperTradingPage() {
  const { language } = useLanguage()
  const zh = language === 'zh'

  const [traders, setTraders] = useState<TraderInfo[]>([])
  const [selectedId, setSelectedId] = useState<string | undefined>()
  const [account, setAccount] = useState<AccountInfo | null>(null)
  const [positions, setPositions] = useState<Position[]>([])
  const [decisions, setDecisions] = useState<DecisionRecord[]>([])
  const [symbol, setSymbol] = useState('BTCUSDT')
  const [interval, setIntervalKey] = useState('5m')
  const [loading, setLoading] = useState(true)
  const [closingKey, setClosingKey] = useState<string>('')
  const [detailDecision, setDetailDecision] = useState<DecisionRecord | null>(null)
  const chartWrapRef = useRef<HTMLDivElement>(null)
  const [chartHeight, setChartHeight] = useState(560)

  // 图表容器高度自适应（AdvancedChart 需要数值高度）
  useEffect(() => {
    const el = chartWrapRef.current
    if (!el) return
    const update = () => setChartHeight(Math.max(320, el.clientHeight - 16))
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const selectedTrader = useMemo(
    () => traders.find((t) => t.trader_id === selectedId),
    [traders, selectedId]
  )

  // 加载模拟盘交易员列表（只显示 paper_trading）
  const loadTraders = useCallback(async () => {
    try {
      const all = await api.getTraders()
      const paper = all.filter((t) => t.paper_trading)
      setTraders(paper)
      setSelectedId((prev) => {
        if (prev && paper.some((t) => t.trader_id === prev)) return prev
        return paper[0]?.trader_id
      })
    } catch (e) {
      console.error('加载模拟盘交易员失败:', e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadTraders()
  }, [loadTraders])

  // 轮询选中交易员的账户/持仓/决策
  useEffect(() => {
    if (!selectedId) {
      setAccount(null)
      setPositions([])
      setDecisions([])
      return
    }
    let alive = true
    const load = async () => {
      try {
        const [acc, pos, dec] = await Promise.all([
          api.getAccount(selectedId),
          api.getPositions(selectedId),
          api.getLatestDecisions(selectedId, 20).catch(() => [] as DecisionRecord[]),
        ])
        if (!alive) return
        setAccount(acc)
        setPositions(Array.isArray(pos) ? pos : [])
        setDecisions(Array.isArray(dec) ? dec : [])
      } catch (e) {
        console.error('刷新模拟盘数据失败:', e)
      }
    }
    load()
    const timer = window.setInterval(load, 8000)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [selectedId])

  const handleClosePosition = async (pos: Position) => {
    if (!selectedId) return
    const key = `${pos.symbol}_${pos.side}`
    if (!window.confirm(zh ? `确认市价平仓 ${pos.symbol} ${pos.side.toUpperCase()}？` : `Close ${pos.symbol} ${pos.side.toUpperCase()} at market?`)) return
    setClosingKey(key)
    try {
      await api.closePosition(selectedId, pos.symbol, pos.side.toUpperCase())
    } catch (e) {
      console.error('平仓失败:', e)
    } finally {
      setClosingKey('')
    }
  }

  const pnlColor = (v: number | undefined) =>
    (v ?? 0) > 0 ? 'text-[#0ECB81]' : (v ?? 0) < 0 ? 'text-[#F6465D]' : 'text-[#848E9C]'

  return (
    <div
      className="h-full flex flex-col overflow-hidden"
      style={{ background: '#0B0E11', color: '#EAECEF' }}
    >
      {/* 顶栏：标题 + 币种 + 周期 */}
      <div className="flex items-center gap-3 px-4 py-2.5 border-b border-[#2B3139] bg-[#16181C] shrink-0 flex-wrap">
        <div className="flex items-center gap-2 mr-2">
          <span className="text-lg">🎮</span>
          <span className="font-bold text-[#EAECEF]">
            {zh ? '本地模拟盘' : 'Paper Trading'}
          </span>
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-[#F0B90B]/10 text-[#F0B90B] border border-[#F0B90B]/30">
            PAPER
          </span>
        </div>
        <select
          value={symbol}
          onChange={(e) => setSymbol(e.target.value)}
          className="px-2.5 py-1.5 bg-[#0B0E11] border border-[#2B3139] rounded text-sm text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
        >
          {SYMBOLS.map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
        <div className="flex gap-1">
          {INTERVALS.map((iv) => (
            <button
              key={iv}
              onClick={() => setIntervalKey(iv)}
              className={`px-2.5 py-1 rounded text-xs transition-colors ${
                interval === iv
                  ? 'bg-[#F0B90B] text-black font-semibold'
                  : 'text-[#848E9C] hover:text-[#EAECEF] hover:bg-[#2B3139]'
              }`}
            >
              {iv}
            </button>
          ))}
        </div>
        <button
          onClick={() => { loadTraders(); if (selectedId) {
            api.getAccount(selectedId).then(setAccount).catch(() => {})
          } }}
          className="ml-auto p-1.5 rounded text-[#848E9C] hover:text-[#EAECEF] hover:bg-[#2B3139] transition-colors"
          title={zh ? '刷新' : 'Refresh'}
        >
          <RefreshCw className="w-4 h-4" />
        </button>
      </div>

      {loading ? (
        <div className="flex-1 flex items-center justify-center">
          <Loader2 className="w-8 h-8 animate-spin text-[#F0B90B]" />
        </div>
      ) : traders.length === 0 ? (
        <div className="flex-1 flex flex-col items-center justify-center gap-2 text-[#848E9C]">
          <span className="text-4xl">🎮</span>
          <p className="text-sm">
            {zh
              ? '暂无模拟盘交易员 — 在「AI 交易员」页创建交易员时勾选「本地模拟盘」即可'
              : 'No paper traders yet — enable "Paper Trading" when creating a trader'}
          </p>
        </div>
      ) : (
        <>
          {/* 主体：图表 + 右侧交易员面板 */}
          <div className="flex-1 flex min-h-0">
            {/* 图表区 */}
            <div ref={chartWrapRef} className="flex-1 min-w-0 p-2 min-h-0">
              <AdvancedChart
                symbol={symbol}
                interval={interval}
                traderID={selectedId}
                exchange="binance"
                height={chartHeight}
              />
            </div>

            {/* 右侧：选中交易员面板 */}
            <div className="w-[360px] shrink-0 border-l border-[#2B3139] flex flex-col min-h-0 bg-[#16181C]">
              {!selectedTrader || !account ? (
                <div className="flex-1 flex items-center justify-center text-sm text-[#848E9C]">
                  {zh ? '在下方选择一位交易员' : 'Select a trader below'}
                </div>
              ) : (
                <>
                  {/* 账户概览 */}
                  <div className="p-3 border-b border-[#2B3139]">
                    <div className="flex items-center justify-between mb-2">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-[#EAECEF]">{selectedTrader.trader_name}</span>
                        <span className={`text-[10px] px-1.5 py-0.5 rounded ${
                          selectedTrader.is_running
                            ? 'bg-[#0ECB81]/10 text-[#0ECB81]'
                            : 'bg-[#848E9C]/10 text-[#848E9C]'
                        }`}>
                          {selectedTrader.is_running ? (zh ? '运行中' : 'Running') : (zh ? '已停止' : 'Stopped')}
                        </span>
                      </div>
                      <span className="text-xs text-[#848E9C]">
                        {selectedTrader.ai_model.replace(/.*_/, '').toUpperCase()}
                      </span>
                    </div>
                    <div className="grid grid-cols-2 gap-2 text-sm">
                      <div className="bg-[#1E2329] rounded p-2">
                        <div className="text-[10px] text-[#848E9C] mb-0.5">{zh ? '账户权益' : 'Equity'}</div>
                        <div className="font-semibold">{fmtUsd(account.total_equity)} <span className="text-[10px] text-[#848E9C]">USDT</span></div>
                      </div>
                      <div className="bg-[#1E2329] rounded p-2">
                        <div className="text-[10px] text-[#848E9C] mb-0.5">{zh ? '总盈亏' : 'Total PnL'}</div>
                        <div className={`font-semibold ${pnlColor(account.total_pnl)}`}>
                          {account.total_pnl >= 0 ? '+' : ''}{fmtUsd(account.total_pnl)} ({fmtUsd(account.total_pnl_pct)}%)
                        </div>
                      </div>
                      <div className="bg-[#1E2329] rounded p-2">
                        <div className="text-[10px] text-[#848E9C] mb-0.5">{zh ? '可用余额' : 'Available'}</div>
                        <div className="font-semibold">{fmtUsd(account.available_balance)}</div>
                      </div>
                      <div className="bg-[#1E2329] rounded p-2">
                        <div className="text-[10px] text-[#848E9C] mb-0.5">{zh ? '未实现盈亏' : 'Unrealized'}</div>
                        <div className={`font-semibold ${pnlColor(account.unrealized_profit)}`}>
                          {account.unrealized_profit >= 0 ? '+' : ''}{fmtUsd(account.unrealized_profit)}
                        </div>
                      </div>
                    </div>
                  </div>

                  {/* 持仓表 */}
                  <div className="px-3 pt-2 pb-1 flex items-center justify-between">
                    <span className="text-xs font-semibold text-[#848E9C]">
                      {zh ? `当前持仓 (${positions.length})` : `Positions (${positions.length})`}
                    </span>
                  </div>
                  <div className="flex-1 min-h-0 overflow-y-auto px-3 space-y-2">
                    {positions.length === 0 ? (
                      <div className="text-xs text-[#848E9C] text-center py-6">
                        {zh ? '暂无持仓' : 'No open positions'}
                      </div>
                    ) : (
                      positions.map((pos) => {
                        const key = `${pos.symbol}_${pos.side}`
                        return (
                          <div key={key} className="bg-[#1E2329] border border-[#2B3139] rounded p-2.5">
                            <div className="flex items-center justify-between mb-1.5">
                              <div className="flex items-center gap-2">
                                <span className={`text-[10px] px-1.5 py-0.5 rounded font-semibold ${
                                  pos.side.toLowerCase() === 'long'
                                    ? 'bg-[#0ECB81]/15 text-[#0ECB81]'
                                    : 'bg-[#F6465D]/15 text-[#F6465D]'
                                }`}>
                                  {pos.side.toUpperCase()}
                                </span>
                                <span className="text-sm font-medium">{pos.symbol}</span>
                                <span className="text-[10px] text-[#848E9C]">{pos.leverage}x</span>
                              </div>
                              <button
                                onClick={() => handleClosePosition(pos)}
                                disabled={closingKey === key}
                                className="text-[10px] px-2 py-0.5 rounded bg-[#F6465D]/15 text-[#F6465D] hover:bg-[#F6465D]/25 transition-colors disabled:opacity-50"
                              >
                                {closingKey === key ? '...' : zh ? '市价平仓' : 'Close'}
                              </button>
                            </div>
                            <div className="grid grid-cols-3 gap-x-2 gap-y-1 text-[11px]">
                              <div>
                                <div className="text-[#848E9C]">{zh ? '开仓价' : 'Entry'}</div>
                                <div>{fmtPrice(pos.entry_price)}</div>
                              </div>
                              <div>
                                <div className="text-[#848E9C]">{zh ? '标记价' : 'Mark'}</div>
                                <div>{fmtPrice(pos.mark_price)}</div>
                              </div>
                              <div>
                                <div className="text-[#848E9C]">{zh ? '强平价' : 'Liq.'}</div>
                                <div className="text-[#F6465D]">{fmtPrice(pos.liquidation_price)}</div>
                              </div>
                              <div>
                                <div className="text-[#848E9C]">{zh ? '数量' : 'Size'}</div>
                                <div>{fmtPrice(pos.quantity)}</div>
                              </div>
                              <div>
                                <div className="text-[#848E9C]">{zh ? '止损' : 'SL'}</div>
                                <div className="text-[#F6465D]">{pos.stop_loss ? fmtPrice(pos.stop_loss) : '—'}</div>
                              </div>
                              <div>
                                <div className="text-[#848E9C]">{zh ? '止盈' : 'TP'}</div>
                                <div className="text-[#0ECB81]">{pos.take_profit ? fmtPrice(pos.take_profit) : '—'}</div>
                              </div>
                            </div>
                            <div className="mt-1.5 pt-1.5 border-t border-[#2B3139] flex justify-between text-[11px]">
                              <span className="text-[#848E9C]">{zh ? '未实现盈亏' : 'Unrealized PnL'}</span>
                              <span className={pnlColor(pos.unrealized_pnl)}>
                                {pos.unrealized_pnl >= 0 ? '+' : ''}{fmtUsd(pos.unrealized_pnl)} ({fmtUsd(pos.unrealized_pnl_pct)}%)
                              </span>
                            </div>
                          </div>
                        )
                      })
                    )}
                  </div>

                  {/* 最近决策 */}
                  <div className="px-3 pt-2 pb-1 border-t border-[#2B3139]">
                    <span className="text-xs font-semibold text-[#848E9C]">
                      {zh ? '最近决策（点击查看思考）' : 'Recent Decisions'}
                    </span>
                  </div>
                  <div className="h-[220px] shrink-0 overflow-y-auto px-3 pb-3 space-y-1.5">
                    {decisions.length === 0 ? (
                      <div className="text-xs text-[#848E9C] text-center py-4">
                        {zh ? '暂无决策记录' : 'No decisions yet'}
                      </div>
                    ) : (
                      decisions.map((d, i) => {
                        const actions = d.decisions || []
                        const main = actions[0]
                        const isTrade = actions.length > 0
                        return (
                          <button
                            key={`${d.cycle_number}_${i}`}
                            onClick={() => setDetailDecision(d)}
                            className="w-full text-left bg-[#1E2329] hover:bg-[#252B35] border border-[#2B3139] rounded px-2.5 py-1.5 transition-colors"
                          >
                            <div className="flex items-center justify-between">
                              <span className="text-[11px]">
                                <span className={isTrade ? 'text-[#F0B90B]' : 'text-[#848E9C]'}>
                                  #{d.cycle_number}
                                </span>
                                <span className="ml-1.5 text-[#EAECEF]">
                                  {main ? `${main.action} ${main.symbol}` : zh ? '观望' : 'Hold'}
                                </span>
                              </span>
                              <span className="text-[10px] text-[#848E9C]">
                                {new Date(d.timestamp).toLocaleString(zh ? 'zh-CN' : 'en-US', {
                                  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
                                })}
                              </span>
                            </div>
                          </button>
                        )
                      })
                    )}
                  </div>
                </>
              )}
            </div>
          </div>

          {/* 底部：交易员卡片区 */}
          <div className="shrink-0 border-t border-[#2B3139] bg-[#16181C] px-3 py-2.5">
            <div className="text-xs font-semibold text-[#848E9C] mb-2">
              {zh ? `模拟盘交易员 (${traders.length})` : `Paper Traders (${traders.length})`}
            </div>
            <div className="flex gap-2.5 overflow-x-auto pb-1">
              {traders.map((t) => {
                const active = t.trader_id === selectedId
                return (
                  <button
                    key={t.trader_id}
                    onClick={() => setSelectedId(t.trader_id)}
                    className={`shrink-0 w-[210px] text-left bg-[#1E2329] border rounded-lg p-3 transition-all ${
                      active
                        ? 'border-[#F0B90B] shadow-[0_0_0_1px_#F0B90B]'
                        : 'border-[#2B3139] hover:border-[#848E9C]'
                    }`}
                  >
                    <div className="flex items-center justify-between mb-1.5">
                      <span className="text-sm font-semibold truncate max-w-[130px]" title={t.trader_name}>
                        {t.trader_name}
                      </span>
                      <span className={`w-2 h-2 rounded-full shrink-0 ${t.is_running ? 'bg-[#0ECB81]' : 'bg-[#848E9C]'}`} />
                    </div>
                    <div className="text-[10px] text-[#848E9C] mb-1">
                      {t.ai_model.replace(/.*_/, '').toUpperCase()}
                      {t.strategy_name ? ` · ${t.strategy_name}` : ''}
                    </div>
                    <div className="text-[11px] text-[#848E9C]">
                      {zh ? '初始资金' : 'Initial'}: {fmtUsd(t.initial_balance)} USDT
                    </div>
                  </button>
                )
              })}
            </div>
          </div>
        </>
      )}

      {/* 决策详情弹窗 */}
      {detailDecision && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-60 backdrop-blur-sm p-4"
          onClick={() => setDetailDecision(null)}
        >
          <div
            className="bg-[#1E2329] border border-[#2B3139] rounded-xl shadow-2xl w-full max-w-3xl max-h-[85vh] flex flex-col"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between p-4 border-b border-[#2B3139] shrink-0">
              <h3 className="font-semibold text-[#EAECEF]">
                {zh ? '决策详情' : 'Decision Detail'} #{detailDecision.cycle_number}
              </h3>
              <button
                onClick={() => setDetailDecision(null)}
                className="w-8 h-8 rounded-lg text-[#848E9C] hover:text-[#EAECEF] hover:bg-[#2B3139] transition-colors flex items-center justify-center"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="p-4 overflow-y-auto min-h-0">
              <DecisionCard decision={detailDecision} language={language} />
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
