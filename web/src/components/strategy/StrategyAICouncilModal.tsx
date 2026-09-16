import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuth } from '../../contexts/AuthContext'
import { useLanguage } from '../../contexts/LanguageContext'
import { t, type Language } from '../../i18n/translations'
import type { StrategyConfig, AIModel } from '../../types'
import { Loader2, X, Sparkles, AlertTriangle, CheckCircle2, XCircle, FileCheck, Link2, Timer } from 'lucide-react'

const API_BASE = import.meta.env.VITE_API_BASE || ''

// ---------- 类型（对应后端 councilState） ----------
interface CouncilStep {
  role: string
  emoji: string
  status: 'pending' | 'running' | 'done' | 'failed' | 'skipped'
  summary?: string
  concerns?: string[]
  payload?: Record<string, unknown>
  sources?: string[]
  error?: string
  duration_ms?: number
  round: number
}

interface CouncilState {
  id: string
  status: 'running' | 'completed' | 'failed' | 'cancelled'
  mode: 'generate' | 'modify'
  intent: string
  language: string
  search_on: boolean
  steps: CouncilStep[]
  result?: {
    config: StrategyConfig
    scan_interval_suggestion: number
    reasoning: string
    clamp_warnings: string[]
    repair_rounds: number
  }
  error?: string
}

interface StrategyAICouncilModalProps {
  open: boolean
  onClose: () => void
  onApply: (config: StrategyConfig) => void
  aiModels: AIModel[]
  defaultModelId: string
  currentConfig: StrategyConfig | null
}

// 步骤展示顺序与轮次分组
const ROUNDS: { round: number; titleKey: string }[] = [
  { round: 1, titleKey: 'round1' },
  { round: 2, titleKey: 'round2' },
  { round: 3, titleKey: 'round3' },
  { round: 4, titleKey: 'round4' },
]

const ROLE_ORDER = [
  'intel_planner', 'market_analyst', 'coin_researcher',
  'chief_trader', 'strategy_architect', 'timeframe_engineer', 'coin_planner',
  'risk_officer', 'chief_reviewer', 'prompt_writer',
]

// ---------- 配置摘要 ----------
function ConfigSummary({ config, lang }: { config: StrategyConfig; lang: Language }) {
  const isZh = lang === 'zh'
  const ind = config.indicators || ({} as StrategyConfig['indicators'])
  const enabledIndicators: string[] = []
  if (ind.enable_ema) enabledIndicators.push(`EMA ${(ind.ema_periods || []).join('/')}`)
  if (ind.enable_rsi) enabledIndicators.push(`RSI ${(ind.rsi_periods || []).join('/')}`)
  if (ind.enable_macd) enabledIndicators.push('MACD')
  if (ind.enable_atr) enabledIndicators.push('ATR')
  if (ind.enable_boll) enabledIndicators.push('BOLL')
  if (ind.enable_volume) enabledIndicators.push(isZh ? '成交量' : 'Volume')
  if (ind.enable_oi) enabledIndicators.push('OI')
  if (ind.enable_funding_rate) enabledIndicators.push(isZh ? '资金费率' : 'Funding')

  const rows: { label: string; value: string }[] = [
    { label: isZh ? '交易模式' : 'Trading Mode', value: config.trading_mode || '-' },
    {
      label: isZh ? '币种来源' : 'Coin Source',
      value:
        (config.coin_source?.source_type || '-') +
        (config.coin_source?.static_coins?.length ? ` (${config.coin_source.static_coins.slice(0, 5).join(', ')}${config.coin_source.static_coins.length > 5 ? '...' : ''})` : ''),
    },
    {
      label: isZh ? '时间周期' : 'Timeframes',
      value: `${config.indicators?.klines?.primary_timeframe || '-'} × ${config.indicators?.klines?.primary_count || '-'}${config.indicators?.klines?.selected_timeframes?.length ? ` · ${(config.indicators.klines.selected_timeframes).join(' / ')}` : ''}`,
    },
    { label: isZh ? '技术指标' : 'Indicators', value: enabledIndicators.join(', ') || (isZh ? '仅原始K线' : 'Raw klines only') },
    {
      label: isZh ? '风控' : 'Risk Control',
      value: isZh
        ? `最大持仓 ${config.risk_control?.max_positions} · 杠杆 BTC/ETH ${config.risk_control?.btc_eth_max_leverage}x / 山寨 ${config.risk_control?.altcoin_max_leverage}x · 信心分 ≥${config.risk_control?.min_confidence}`
        : `Max positions ${config.risk_control?.max_positions} · Leverage BTC/ETH ${config.risk_control?.btc_eth_max_leverage}x / Alt ${config.risk_control?.altcoin_max_leverage}x · Conf ≥${config.risk_control?.min_confidence}`,
    },
  ]

  return (
    <div className="space-y-1.5">
      {rows.map((r) => (
        <div key={r.label} className="flex gap-2 text-[11px]">
          <span className="w-20 shrink-0 text-[#848E9C]">{r.label}</span>
          <span className="text-[#EAECEF] break-words">{r.value}</span>
        </div>
      ))}
    </div>
  )
}

// ---------- 步骤状态图标 ----------
function StepStatusBadge({ status, lang }: { status: CouncilStep['status']; lang: Language }) {
  const cls = 'flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded'
  switch (status) {
    case 'running':
      return (
        <span className={`${cls} bg-amber-500/10 text-amber-400`}>
          <Loader2 className="w-3 h-3 animate-spin" />
          {t('aiCouncil.runningStep', lang)}
        </span>
      )
    case 'done':
      return (
        <span className={`${cls} bg-[#0ECB81]/10 text-[#0ECB81]`}>
          <CheckCircle2 className="w-3 h-3" />
          {t('aiCouncil.doneStep', lang)}
        </span>
      )
    case 'failed':
      return (
        <span className={`${cls} bg-[#F6465D]/10 text-[#F6465D]`}>
          <XCircle className="w-3 h-3" />
          {t('aiCouncil.failedStep', lang)}
        </span>
      )
    case 'skipped':
      return <span className={`${cls} bg-[#2B3139] text-[#848E9C]`}>{t('aiCouncil.skippedStep', lang)}</span>
    default:
      return <span className={`${cls} bg-[#2B3139] text-[#848E9C]`}>{t('aiCouncil.pending', lang)}</span>
  }
}

// ---------- 主组件 ----------
export function StrategyAICouncilModal({ open, onClose, onApply, aiModels, defaultModelId, currentConfig }: StrategyAICouncilModalProps) {
  const { token } = useAuth()
  const { language } = useLanguage()
  const [intent, setIntent] = useState('')
  const [modelId, setModelId] = useState(defaultModelId)
  const [council, setCouncil] = useState<CouncilState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [applied, setApplied] = useState(false)
  const [starting, setStarting] = useState(false)
  const [mode, setMode] = useState<'generate' | 'modify'>(currentConfig ? 'modify' : 'generate')
  // running 步骤的本地起始时间（role → 时间戳），用于显示已用时长
  const stepStartRef = useRef<Map<string, number>>(new Map())
  const [nowTick, setNowTick] = useState(Date.now())
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const councilIdRef = useRef<string | null>(null)

  // 弹窗打开时按当前配置重置模式
  useEffect(() => {
    if (open) setMode(currentConfig ? 'modify' : 'generate')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // 运行中每秒刷新计时显示
  useEffect(() => {
    if (council?.status !== 'running') return
    const timer = setInterval(() => setNowTick(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [council?.status])

  // 记录/清理 running 步骤的本地起始时间
  const applyCouncilData = useCallback((data: CouncilState) => {
    const now = Date.now()
    const map = stepStartRef.current
    for (const s of data.steps) {
      if (s.status === 'running') {
        if (!map.has(s.role)) map.set(s.role, now)
      } else {
        map.delete(s.role)
      }
    }
    setCouncil(data)
  }, [])

  // 清除轮询
  const stopPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current)
      pollTimerRef.current = null
    }
  }, [])

  // 轮询会诊状态
  const startPolling = useCallback(
    (id: string) => {
      stopPolling()
      pollTimerRef.current = setInterval(async () => {
        try {
          const resp = await fetch(`${API_BASE}/api/strategies/ai-council/${id}`, {
            headers: { Authorization: `Bearer ${token}` },
          })
          if (!resp.ok) return
          const data: CouncilState = await resp.json()
          applyCouncilData(data)
          if (data.status !== 'running') stopPolling()
        } catch {
          // 网络抖动忽略，下一轮重试
        }
      }, 2000)
    },
    [token, stopPolling, applyCouncilData]
  )

  // 弹窗关闭时停止轮询
  useEffect(() => {
    if (!open) {
      stopPolling()
    }
    return () => stopPolling()
  }, [open, stopPolling])

  if (!open) return null

  const startCouncil = async () => {
    if (!modelId) {
      setError(t('aiCouncil.needModel', language))
      return
    }
    if (!intent.trim()) {
      setError(t('aiCouncil.needIntent', language))
      return
    }
    setError(null)
    setApplied(false)
    setStarting(true)
    try {
      const resp = await fetch(`${API_BASE}/api/strategies/ai-council`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({
          intent: intent.trim(),
          model_id: modelId,
          language,
          mode,
          config: mode === 'modify' ? currentConfig : undefined,
        }),
      })
      const data = await resp.json()
      if (!resp.ok) throw new Error(data.error || 'Failed to start council')
      councilIdRef.current = data.council_id
      setCouncil({ id: data.council_id, status: 'running', mode, intent, language, search_on: false, steps: [] })
      startPolling(data.council_id)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setStarting(false)
    }
  }

  const cancelCouncil = async () => {
    if (!councilIdRef.current) return
    try {
      await fetch(`${API_BASE}/api/strategies/ai-council/${councilIdRef.current}/cancel`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}` },
      })
    } catch {
      // 忽略
    }
  }

  const handleApply = () => {
    if (council?.result?.config) {
      onApply(council.result.config)
      setApplied(true)
    }
  }

  const backToForm = () => {
    stopPolling()
    setCouncil(null)
    councilIdRef.current = null
  }

  const steps = council?.steps || []
  const orderedSteps = [...steps].sort((a, b) => ROLE_ORDER.indexOf(a.role) - ROLE_ORDER.indexOf(b.role))
  const isRunning = council?.status === 'running'

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={onClose}>
      <div
        className="w-full max-w-2xl max-h-[85vh] mx-4 flex flex-col rounded-xl overflow-hidden shadow-2xl"
        style={{ background: '#16181C', border: '1px solid #2B3139' }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* 标题栏 */}
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-[#2B3139] shrink-0">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-amber-400 to-amber-600 flex items-center justify-center">
              <Sparkles className="w-4.5 h-4.5 text-[#16181C]" />
            </div>
            <div>
              <h3 className="text-sm font-semibold text-[#EAECEF]">{t('aiCouncil.title', language)}</h3>
              <p className="text-[10px] text-[#848E9C]">{t('aiCouncil.subtitle', language)}</p>
            </div>
          </div>
          <button onClick={onClose} className="p-1.5 rounded-lg text-[#848E9C] hover:text-[#EAECEF] hover:bg-[#1E2329] transition-colors">
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* 内容滚动区 */}
        <div className="flex-1 overflow-y-auto px-5 py-4 space-y-4">
          {/* 表单阶段 */}
          {!council && (
            <>
              {/* 模式切换：生成 / 修改 */}
              <div className="flex items-center gap-2">
                <span className="text-[11px] text-[#848E9C] shrink-0">{t('aiCouncil.modeLabel', language)}</span>
                <div className="flex rounded-lg overflow-hidden" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
                  <button
                    onClick={() => setMode('generate')}
                    className={`px-3 py-1 text-[11px] transition-colors ${
                      mode === 'generate' ? 'bg-amber-500/15 text-amber-400 font-medium' : 'text-[#848E9C] hover:text-[#EAECEF]'
                    }`}
                  >
                    {t('aiCouncil.modeGenerate', language)}
                  </button>
                  <button
                    onClick={() => setMode('modify')}
                    disabled={!currentConfig}
                    title={!currentConfig ? (language === 'zh' ? '当前策略为空，无法修改' : 'No current strategy to modify') : undefined}
                    className={`px-3 py-1 text-[11px] transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
                      mode === 'modify' ? 'bg-amber-500/15 text-amber-400 font-medium' : 'text-[#848E9C] hover:text-[#EAECEF]'
                    }`}
                  >
                    {t('aiCouncil.modeModify', language)}
                  </button>
                </div>
              </div>

              <div>
                <label className="block text-[11px] text-[#848E9C] mb-1.5">{t('aiCouncil.modelLabel', language)}</label>
                <select
                  value={modelId}
                  onChange={(e) => setModelId(e.target.value)}
                  className="w-full px-3 py-2 rounded-lg text-[12px] text-[#EAECEF] focus:outline-none focus:border-amber-500/50"
                  style={{ background: '#1E2329', border: '1px solid #2B3139' }}
                >
                  <option value="">--</option>
                  {aiModels.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name}
                    </option>
                  ))}
                </select>
              </div>

              <div>
                <label className="block text-[11px] text-[#848E9C] mb-1.5">{t('aiCouncil.intentLabel', language)}</label>
                <textarea
                  value={intent}
                  onChange={(e) => setIntent(e.target.value)}
                  placeholder={t('aiCouncil.intentPlaceholder', language)}
                  rows={6}
                  className="w-full px-3 py-2.5 rounded-lg text-[12px] text-[#EAECEF] placeholder-[#5C6470] resize-y focus:outline-none focus:border-amber-500/50"
                  style={{ background: '#1E2329', border: '1px solid #2B3139' }}
                />
              </div>
            </>
          )}

          {/* 会诊过程 */}
          {council && (
            <>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  {isRunning && <Loader2 className="w-3.5 h-3.5 animate-spin text-amber-400" />}
                  <span className="text-[12px] text-[#EAECEF] font-medium">{t('aiCouncil.running', language)}</span>
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-[#1E2329] text-[#848E9C]">
                    {t(council.mode === 'modify' ? 'aiCouncil.modeModify' : 'aiCouncil.modeGenerate', language)}
                  </span>
                </div>
                {isRunning && (
                  <button onClick={cancelCouncil} className="text-[11px] text-[#F6465D] hover:underline">
                    {t('aiCouncil.cancel', language)}
                  </button>
                )}
              </div>

              {ROUNDS.map(({ round, titleKey }) => {
                const roundSteps = orderedSteps.filter((s) => s.round === round)
                if (roundSteps.length === 0) return null
                return (
                  <div key={round} className="space-y-2">
                    <div className="text-[10px] font-semibold text-amber-400/90 uppercase tracking-wider">{t(`aiCouncil.${titleKey}`, language)}</div>
                    <div className="space-y-2">
                      {roundSteps.map((step) => (
                        <div key={step.role} className="rounded-lg p-3" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
                          <div className="flex items-center justify-between">
                            <div className="flex items-center gap-2 text-[12px] text-[#EAECEF] font-medium">
                              <span>{step.emoji}</span>
                              {t(`aiCouncil.roles.${step.role}`, language)}
                            </div>
                            <div className="flex items-center gap-2">
                              {step.status === 'running' && stepStartRef.current.has(step.role) ? (
                                <span className="flex items-center gap-0.5 text-[10px] text-amber-400 tabular-nums">
                                  <Timer className="w-3 h-3" />
                                  {Math.max(0, Math.floor((nowTick - (stepStartRef.current.get(step.role) || 0)) / 1000))}s
                                </span>
                              ) : step.duration_ms ? (
                                <span className="flex items-center gap-0.5 text-[10px] text-[#848E9C]">
                                  <Timer className="w-3 h-3" />
                                  {(step.duration_ms / 1000).toFixed(0)}s
                                </span>
                              ) : null}
                              <StepStatusBadge status={step.status} lang={language} />
                            </div>
                          </div>
                          {step.summary && <p className="mt-1.5 text-[11px] text-[#B7BDC6] leading-relaxed">{step.summary}</p>}
                          {step.error && <p className="mt-1.5 text-[11px] text-[#F6465D]">{step.error}</p>}
                          {step.concerns && step.concerns.length > 0 && (
                            <div className="mt-2 space-y-1">
                              <span className="text-[10px] text-amber-400">{t('aiCouncil.concerns', language)}</span>
                              {step.concerns.map((c, i) => (
                                <div key={i} className="text-[10px] text-[#B7BDC6] bg-amber-500/5 border border-amber-500/20 rounded px-2 py-1">
                                  {c}
                                </div>
                              ))}
                            </div>
                          )}
                          {step.sources && step.sources.length > 0 && (
                            <div className="mt-2 flex flex-wrap items-center gap-1">
                              <Link2 className="w-3 h-3 text-[#848E9C]" />
                              {step.sources.slice(0, 4).map((s, i) => (
                                <span key={i} className="text-[10px] text-[#848E9C] bg-[#16181C] rounded px-1.5 py-0.5 max-w-[180px] truncate">
                                  {s}
                                </span>
                              ))}
                              {step.sources.length > 4 && <span className="text-[10px] text-[#848E9C]">+{step.sources.length - 4}</span>}
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                )
              })}

              {/* 失败 */}
              {council.status === 'failed' && (
                <div className="rounded-lg p-3 bg-[#F6465D]/10 border border-[#F6465D]/30 flex items-start gap-2">
                  <AlertTriangle className="w-4 h-4 text-[#F6465D] shrink-0 mt-0.5" />
                  <div>
                    <p className="text-[12px] text-[#F6465D] font-medium">{t('aiCouncil.councilFailed', language)}</p>
                    <p className="text-[11px] text-[#B7BDC6] mt-0.5">{council.error}</p>
                  </div>
                </div>
              )}
              {council.status === 'cancelled' && (
                <div className="rounded-lg p-3 bg-[#2B3139]/50 border border-[#2B3139] text-[11px] text-[#848E9C]">
                  {language === 'zh' ? '会诊已取消。' : 'Council cancelled.'}
                </div>
              )}

              {/* 结果 */}
              {council.status === 'completed' && council.result && (
                <div className="rounded-lg p-3.5 space-y-3" style={{ background: '#1E2329', border: '1px solid rgba(240,185,11,0.35)' }}>
                  <div className="flex items-center gap-2">
                    <FileCheck className="w-4 h-4 text-amber-400" />
                    <span className="text-[13px] font-semibold text-[#EAECEF]">{t('aiCouncil.resultTitle', language)}</span>
                  </div>

                  {council.result.reasoning && (
                    <div>
                      <div className="text-[10px] text-[#848E9C] mb-1">{t('aiCouncil.reasoning', language)}</div>
                      <p className="text-[11px] text-[#B7BDC6] leading-relaxed">{council.result.reasoning}</p>
                    </div>
                  )}

                  <ConfigSummary config={council.result.config} lang={language} />

                  {council.result.config?.prompt_sections?.role_definition && (
                    <div className="rounded-lg bg-amber-500/5 border border-amber-500/25 px-3 py-2">
                      <div className="flex items-center gap-1.5 text-[11px] text-amber-400">
                        <Sparkles className="w-3.5 h-3.5" />
                        {t('aiCouncil.promptGenerated', language)}
                      </div>
                      <p className="text-[10px] text-[#848E9C] mt-0.5">{t('aiCouncil.promptGeneratedNote', language)}</p>
                    </div>
                  )}

                  {council.result.scan_interval_suggestion > 0 && (
                    <div className="rounded-lg bg-amber-500/5 border border-amber-500/25 px-3 py-2">
                      <div className="flex items-center gap-1.5 text-[11px] text-amber-400">
                        <Timer className="w-3.5 h-3.5" />
                        {t('aiCouncil.scanSuggestion', language)}: {council.result.scan_interval_suggestion} {t('aiCouncil.minutes', language)}
                      </div>
                      <p className="text-[10px] text-[#848E9C] mt-0.5">{t('aiCouncil.scanSuggestionNote', language)}</p>
                    </div>
                  )}

                  {council.result.clamp_warnings?.length > 0 && (
                    <div>
                      <div className="text-[10px] text-[#848E9C] mb-1">
                        {t('aiCouncil.clampWarnings', language)}
                        {council.result.repair_rounds > 0 ? ` · ${t('aiCouncil.repairRounds', language)}: ${council.result.repair_rounds}` : ''}
                      </div>
                      <div className="space-y-0.5">
                        {council.result.clamp_warnings.map((w, i) => (
                          <p key={i} className="text-[10px] text-[#B7BDC6]">• {w}</p>
                        ))}
                      </div>
                    </div>
                  )}

                  <button
                    onClick={handleApply}
                    disabled={applied}
                    className={`w-full py-2 rounded-lg text-[12px] font-medium transition-all ${
                      applied ? 'bg-[#0ECB81]/15 text-[#0ECB81]' : 'bg-gradient-to-r from-amber-400 to-amber-500 text-[#16181C] hover:opacity-90'
                    }`}
                  >
                    {applied ? t('aiCouncil.applied', language) : t('aiCouncil.apply', language)}
                  </button>
                </div>
              )}
            </>
          )}

          {error && (
            <div className="rounded-lg px-3 py-2 bg-[#F6465D]/10 border border-[#F6465D]/30 text-[11px] text-[#F6465D]">{error}</div>
          )}
        </div>

        {/* 底部按钮 */}
        <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-[#2B3139] shrink-0">
          {council && council.status !== 'running' && (
            <button
              onClick={backToForm}
              className="px-4 py-1.5 rounded-lg text-[12px] text-[#848E9C] hover:text-[#EAECEF] transition-colors"
              style={{ background: '#1E2329' }}
            >
              {language === 'zh' ? '返回' : 'Back'}
            </button>
          )}
          {!council && (
            <button
              onClick={startCouncil}
              disabled={starting}
              className="flex items-center gap-1.5 px-5 py-1.5 rounded-lg text-[12px] font-medium bg-gradient-to-r from-amber-400 to-amber-500 text-[#16181C] hover:opacity-90 transition-all disabled:opacity-50"
            >
              {starting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Sparkles className="w-3.5 h-3.5" />}
              {t('aiCouncil.start', language)}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
