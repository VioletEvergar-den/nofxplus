import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuth } from '../../contexts/AuthContext'
import { useLanguage } from '../../contexts/LanguageContext'
import { t, type Language } from '../../i18n/translations'
import type { StrategyConfig, AIModel } from '../../types'
import {
  Loader2, X, Sparkles, AlertTriangle, CheckCircle2, XCircle, FileCheck,
  Timer, Wrench, MessageSquare, Scale, Search, PenLine, Crosshair, DraftingCompass,
} from 'lucide-react'

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
  activity?: string[]
  error?: string
  duration_ms?: number
  round: number
}

interface TranscriptEntry {
  role: string
  emoji: string
  name: string
  kind: 'speech' | 'tool_call' | 'tool_result' | 'question' | 'answer' | 'system'
  content: string
  payload?: Record<string, unknown>
  created_at: string
}

interface CouncilState {
  id: string
  status: 'running' | 'completed' | 'failed' | 'cancelled'
  mode: 'generate' | 'modify'
  intent: string
  language: string
  search_on: boolean
  budget?: number
  used_budget?: number
  steps: CouncilStep[]
  transcript?: TranscriptEntry[]
  result?: {
    config: StrategyConfig
    strategy_name?: string
    strategy_description?: string
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
  onCreateStrategy?: (name: string, description: string, config: StrategyConfig) => Promise<boolean>
  aiModels: AIModel[]
  defaultModelId: string
  currentConfig: StrategyConfig | null
}

// Agent 圆桌角色顺序（3 轮 5 角色）
const ROLE_ORDER = [
  'intel_analyst', 'chief_trader', 'strategy_architect', 'risk_reviewer', 'prompt_writer',
]

const ROLE_ICONS: Record<string, typeof Search> = {
  intel_analyst: Search,
  chief_trader: Crosshair,
  strategy_architect: DraftingCompass,
  risk_reviewer: Scale,
  prompt_writer: PenLine,
}

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

// ---------- 实时消息流单条渲染 ----------
function TranscriptItem({ entry, lang }: { entry: TranscriptEntry; lang: Language }) {
  const time = new Date(entry.created_at)
  const timeStr = isNaN(time.getTime()) ? '' : time.toLocaleTimeString('zh-CN', { hour12: false })

  // 系统消息：居中灰条
  if (entry.kind === 'system') {
    const isWarn = entry.content.includes('错误')
    return (
      <div className="flex justify-center py-0.5">
        <div
          className="text-[10px] px-2.5 py-1 rounded-full max-w-[85%] text-center break-words"
          style={{
            background: isWarn ? 'rgba(246,70,85,0.08)' : '#1E2329',
            border: `1px solid ${isWarn ? 'rgba(246,70,85,0.25)' : '#2B3139'}`,
            color: isWarn ? '#F6465D' : '#848E9C',
          }}
        >
          {entry.emoji} {entry.content}
        </div>
      </div>
    )
  }

  // 工具调用：amber 描边小卡
  if (entry.kind === 'tool_call') {
    return (
      <div className="flex items-start gap-2">
        <div className="w-6 h-6 rounded-full flex items-center justify-center shrink-0 mt-0.5" style={{ background: 'rgba(240,185,11,0.12)', border: '1px solid rgba(240,185,11,0.4)' }}>
          <Wrench className="w-3 h-3 text-amber-400" />
        </div>
        <div className="rounded-lg px-2.5 py-1.5 flex-1 min-w-0" style={{ background: 'rgba(240,185,11,0.06)', border: '1px solid rgba(240,185,11,0.3)' }}>
          <div className="text-[10px] text-amber-400 font-medium flex items-center gap-1.5">
            <span>{entry.emoji} {entry.name}</span>
            <span className="text-amber-400/50">{timeStr}</span>
          </div>
          <div className="text-[11px] text-[#EAECEF] font-mono mt-0.5 break-all">{entry.content}</div>
        </div>
      </div>
    )
  }

  // 工具结果：可折叠数据块
  if (entry.kind === 'tool_result') {
    const firstLine = entry.content.split('\n')[0] || ''
    return (
      <details className="ml-8 group">
        <summary className="cursor-pointer select-none text-[10px] text-[#848E9C] hover:text-[#B7BDC6] flex items-center gap-1.5 py-0.5 list-none">
          <span className="transition-transform group-open:rotate-90">▸</span>
          <span className="font-mono truncate max-w-[480px]" style={{ color: '#0ECB81' }}>📊 {firstLine}</span>
        </summary>
        <pre className="mt-1 rounded-lg p-2.5 text-[10px] font-mono whitespace-pre-wrap break-all max-h-56 overflow-y-auto" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#B7BDC6' }}>
          {entry.content}
        </pre>
      </details>
    )
  }

  // 提问：专家互问（amber/红 描边卡）
  if (entry.kind === 'question') {
    return (
      <div className="flex items-start gap-2">
        <div className="w-6 h-6 rounded-full flex items-center justify-center shrink-0 mt-0.5" style={{ background: 'rgba(246,70,85,0.1)', border: '1px solid rgba(246,70,85,0.4)' }}>
          <MessageSquare className="w-3 h-3 text-[#F6465D]" />
        </div>
        <div className="rounded-lg px-2.5 py-1.5 flex-1 min-w-0" style={{ background: 'rgba(246,70,85,0.05)', border: '1px solid rgba(246,70,85,0.35)' }}>
          <div className="text-[10px] text-[#F6465D] font-medium flex items-center gap-1.5">
            <MessageSquare className="w-3 h-3" />
            <span>{entry.emoji} {entry.name} · {lang === 'zh' ? '向其他专家提问' : 'Cross-question'}</span>
            <span className="text-[#F6465D]/50">{timeStr}</span>
          </div>
          <div className="text-[11px] text-[#EAECEF] mt-0.5 leading-relaxed whitespace-pre-wrap break-words">{entry.content}</div>
        </div>
      </div>
    )
  }

  // speech / answer：角色气泡
  const isAnswer = entry.kind === 'answer'
  return (
    <div className="flex items-start gap-2">
      <div
        className="w-7 h-7 rounded-full flex items-center justify-center shrink-0 mt-0.5 text-[13px]"
        style={{ background: '#1E2329', border: '1px solid rgba(240,185,11,0.35)' }}
      >
        {entry.emoji}
      </div>
      <div className="flex-1 min-w-0 rounded-lg px-3 py-2" style={{ background: '#1E2329', border: isAnswer ? '1px solid rgba(240,185,11,0.4)' : '1px solid #2B3139' }}>
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-[11px] font-semibold text-[#EAECEF]">{entry.name}</span>
          {isAnswer && (
            <span className="text-[9px] px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400">
              {lang === 'zh' ? '回应追问' : 'Response'}
            </span>
          )}
          <span className="text-[9px] text-[#5C6470]">{timeStr}</span>
        </div>
        <div className="text-[11.5px] text-[#D1D5DB] mt-1 leading-relaxed whitespace-pre-wrap break-words">{entry.content}</div>
        {entry.payload && Object.keys(entry.payload).length > 0 && (
          <details className="mt-1.5 group">
            <summary className="cursor-pointer select-none text-[10px] text-[#848E9C] hover:text-amber-400 flex items-center gap-1 list-none">
              <span className="transition-transform group-open:rotate-90">▸</span>
              {lang === 'zh' ? '结构化产出' : 'Structured payload'}
            </summary>
            <pre className="mt-1 rounded-lg p-2 text-[9.5px] font-mono whitespace-pre-wrap break-all max-h-44 overflow-y-auto" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#8FA3B8' }}>
              {JSON.stringify(entry.payload, null, 2)}
            </pre>
          </details>
        )}
      </div>
    </div>
  )
}

// ---------- 主组件 ----------
export function StrategyAICouncilModal({ open, onClose, onApply, onCreateStrategy, aiModels, defaultModelId, currentConfig }: StrategyAICouncilModalProps) {
  const { token } = useAuth()
  const { language } = useLanguage()
  const [intent, setIntent] = useState('')
  const [modelId, setModelId] = useState(defaultModelId)
  const [council, setCouncil] = useState<CouncilState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [applied, setApplied] = useState(false)
  const [starting, setStarting] = useState(false)
  const [agentBudget, setAgentBudget] = useState(12)
  const [capital, setCapital] = useState('')
  const [promptStyle, setPromptStyle] = useState<'auto' | 'concise' | 'balanced' | 'detailed'>('auto')
  const [creating, setCreating] = useState(false)
  const [mode, setMode] = useState<'generate' | 'modify'>(currentConfig ? 'modify' : 'generate')
  // running 步骤的本地起始时间（role → 时间戳），用于显示已用时长
  const stepStartRef = useRef<Map<string, number>>(new Map())
  const [nowTick, setNowTick] = useState(Date.now())
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const councilIdRef = useRef<string | null>(null)
  const feedRef = useRef<HTMLDivElement | null>(null)

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

  // 消息流自动滚到底部（新消息到达时）
  const transcriptLen = council?.transcript?.length || 0
  useEffect(() => {
    const el = feedRef.current
    if (!el) return
    // 距底部 120px 内才自动跟随，避免用户回看时被拽走
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 160) {
      el.scrollTop = el.scrollHeight
    }
  }, [transcriptLen])

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

  // 轮询会诊状态（1.5s，实时感更强）
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
      }, 1500)
    },
    [token, stopPolling, applyCouncilData]
  )

  // 弹窗打开时锁定 body 滚动（防止滚轮穿透到后面的页面）
  useEffect(() => {
    if (!open) return
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = prevOverflow
    }
  }, [open])

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
          agent_budget: agentBudget,
          capital: capital.trim() ? Number(capital) : 0,
          prompt_style: promptStyle,
          config: mode === 'modify' ? currentConfig : undefined,
        }),
      })
      const data = await resp.json()
      if (!resp.ok) throw new Error(data.error || 'Failed to start council')
      councilIdRef.current = data.council_id
      setCouncil({ id: data.council_id, status: 'running', mode, intent, language, search_on: false, steps: [], transcript: [] })
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

  // 生成模式：用 AI 取的名字直接创建新策略到左侧列表
  const handleAddToList = async () => {
    if (!council?.result?.config || !onCreateStrategy) return
    setCreating(true)
    try {
      const name = council.result.strategy_name || (language === 'zh' ? 'AI 专家团策略' : 'AI Council Strategy')
      const ok = await onCreateStrategy(name, council.result.strategy_description || '', council.result.config)
      if (ok) setApplied(true)
    } finally {
      setCreating(false)
    }
  }

  const backToForm = () => {
    stopPolling()
    setCouncil(null)
    councilIdRef.current = null
  }

  const steps = council?.steps || []
  const transcript = council?.transcript || []
  const isRunning = council?.status === 'running'
  const usedBudget = council?.used_budget ?? 0
  const totalBudget = council?.budget ?? agentBudget
  const budgetPct = totalBudget > 0 ? Math.min(100, Math.round((usedBudget / totalBudget) * 100)) : 0
  const doneCount = steps.filter((s) => s.status === 'done').length

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/75 backdrop-blur-sm p-4" onClick={onClose}>
      <div
        className="w-full max-w-6xl h-[90vh] flex flex-col rounded-2xl overflow-hidden shadow-2xl"
        style={{ background: '#16181C', border: '1px solid #2B3139' }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* 标题栏 */}
        <div className="flex items-center justify-between px-5 py-3 border-b border-[#2B3139] shrink-0">
          <div className="flex items-center gap-3">
            <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-amber-400 to-amber-600 flex items-center justify-center shadow-lg shadow-amber-500/20">
              <Sparkles className="w-4.5 h-4.5 text-[#16181C]" />
            </div>
            <div>
              <h3 className="text-[15px] font-bold text-[#EAECEF]">{t('aiCouncil.title', language)}</h3>
              <p className="text-[10px] text-[#848E9C]">{t('aiCouncil.subtitle', language)}</p>
            </div>
            {council && (
              <div className="flex items-center gap-2 ml-3">
                {isRunning && <Loader2 className="w-3.5 h-3.5 animate-spin text-amber-400" />}
                <span className="text-[10px] px-2 py-0.5 rounded bg-[#1E2329] text-[#848E9C]" style={{ border: '1px solid #2B3139' }}>
                  {t(council.mode === 'modify' ? 'aiCouncil.modeModify' : 'aiCouncil.modeGenerate', language)}
                </span>
                <span className="text-[10px] px-2 py-0.5 rounded bg-amber-500/10 text-amber-400 tabular-nums" style={{ border: '1px solid rgba(240,185,11,0.3)' }}>
                  {t('aiCouncil.budgetUsage', language)} {usedBudget}/{totalBudget}
                </span>
                {isRunning && (
                  <button onClick={cancelCouncil} className="text-[11px] text-[#F6465D] hover:underline">
                    {t('aiCouncil.cancel', language)}
                  </button>
                )}
              </div>
            )}
          </div>
          <button onClick={onClose} className="p-1.5 rounded-lg text-[#848E9C] hover:text-[#EAECEF] hover:bg-[#1E2329] transition-colors">
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* ============ 阶段 A：发起表单 ============ */}
        {!council && (
          <div className="flex-1 overflow-y-auto p-6">
            <div className="grid grid-cols-1 lg:grid-cols-5 gap-5 h-full">
              {/* 左：表单（3/5） */}
              <div className="lg:col-span-3 space-y-4">
                {/* 模式切换 */}
                <div className="flex items-center gap-2.5">
                  <span className="text-[11px] text-[#848E9C] shrink-0">{t('aiCouncil.modeLabel', language)}</span>
                  <div className="flex rounded-lg overflow-hidden" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
                    <button
                      onClick={() => setMode('generate')}
                      className={`px-3.5 py-1.5 text-[11px] transition-colors ${
                        mode === 'generate' ? 'bg-amber-500/15 text-amber-400 font-medium' : 'text-[#848E9C] hover:text-[#EAECEF]'
                      }`}
                    >
                      {t('aiCouncil.modeGenerate', language)}
                    </button>
                    <button
                      onClick={() => setMode('modify')}
                      disabled={!currentConfig}
                      title={!currentConfig ? (language === 'zh' ? '当前策略为空，无法修改' : 'No current strategy to modify') : undefined}
                      className={`px-3.5 py-1.5 text-[11px] transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
                        mode === 'modify' ? 'bg-amber-500/15 text-amber-400 font-medium' : 'text-[#848E9C] hover:text-[#EAECEF]'
                      }`}
                    >
                      {t('aiCouncil.modeModify', language)}
                    </button>
                  </div>
                </div>

                {/* 意图 */}
                <div>
                  <label className="block text-[11px] text-[#848E9C] mb-1.5">{t('aiCouncil.intentLabel', language)}</label>
                  <textarea
                    value={intent}
                    onChange={(e) => setIntent(e.target.value)}
                    placeholder={t('aiCouncil.intentPlaceholder', language)}
                    rows={9}
                    className="w-full px-3.5 py-3 rounded-xl text-[12px] leading-relaxed text-[#EAECEF] placeholder-[#5C6470] resize-y focus:outline-none focus:border-amber-500/50"
                    style={{ background: '#1E2329', border: '1px solid #2B3139' }}
                  />
                </div>

                {/* 提示词风格 */}
                <div>
                  <label className="block text-[11px] text-[#848E9C] mb-1.5">{t('aiCouncil.styleLabel', language)}</label>
                  <div className="grid grid-cols-4 gap-1.5">
                    {(['auto', 'concise', 'balanced', 'detailed'] as const).map((s) => (
                      <button
                        key={s}
                        onClick={() => setPromptStyle(s)}
                        title={t(`aiCouncil.styleHint.${s}`, language)}
                        className={`px-2 py-1.5 rounded-lg text-[11px] transition-all ${
                          promptStyle === s
                            ? 'bg-amber-500/15 text-amber-400 font-medium border border-amber-500/50'
                            : 'text-[#848E9C] hover:text-[#EAECEF] border border-[#2B3139]'
                        }`}
                        style={promptStyle === s ? undefined : { background: '#1E2329' }}
                      >
                        {t(`aiCouncil.styleOption.${s}`, language)}
                      </button>
                    ))}
                  </div>
                  <p className="text-[9px] text-[#5C6470] mt-1 leading-tight">{t(`aiCouncil.styleHint.${promptStyle}`, language)}</p>
                </div>

                <div className="grid grid-cols-3 gap-3">
                  {/* 模型 */}
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
                  {/* 本金 */}
                  <div>
                    <label className="block text-[11px] text-[#848E9C] mb-1.5">{t('aiCouncil.capitalLabel', language)}</label>
                    <input
                      type="number"
                      min={0}
                      step="any"
                      value={capital}
                      onChange={(e) => setCapital(e.target.value)}
                      placeholder={t('aiCouncil.capitalPlaceholder', language)}
                      className="w-full px-3 py-2 rounded-lg text-[12px] text-[#EAECEF] placeholder-[#5C6470] focus:outline-none focus:border-amber-500/50"
                      style={{ background: '#1E2329', border: '1px solid #2B3139' }}
                    />
                  </div>
                  {/* 预算 */}
                  <div>
                    <label className="block text-[11px] text-[#848E9C] mb-1.5">{t('aiCouncil.budgetLabel', language)}</label>
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        min={5}
                        max={200}
                        value={agentBudget}
                        onChange={(e) => setAgentBudget(Math.max(5, Math.min(200, Number(e.target.value) || 12)))}
                        className="w-16 px-2.5 py-2 rounded-lg text-[12px] text-[#EAECEF] focus:outline-none focus:border-amber-500/50"
                        style={{ background: '#1E2329', border: '1px solid #2B3139' }}
                      />
                    </div>
                    <p className="text-[9px] text-[#5C6470] mt-1 leading-tight">{t('aiCouncil.budgetHint', language)}</p>
                  </div>
                </div>

                {error && (
                  <div className="rounded-lg px-3 py-2 bg-[#F6465D]/10 border border-[#F6465D]/30 text-[11px] text-[#F6465D]">{error}</div>
                )}

                {/* 发起按钮 */}
                <button
                  onClick={startCouncil}
                  disabled={starting}
                  className="w-full py-3 rounded-xl text-[13px] font-bold bg-gradient-to-r from-amber-400 to-amber-500 text-[#16181C] hover:opacity-90 transition-all disabled:opacity-50 shadow-lg shadow-amber-500/20 flex items-center justify-center gap-2"
                >
                  {starting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Sparkles className="w-4 h-4" />}
                  {t('aiCouncil.start', language)}
                </button>
              </div>

              {/* 右：专家团阵容（2/5） */}
              <div className="lg:col-span-2">
                <div className="text-[11px] font-semibold text-amber-400 uppercase tracking-wider mb-2.5 flex items-center gap-1.5">
                  <Sparkles className="w-3.5 h-3.5" />
                  {t('aiCouncil.meetTeam', language)}
                </div>
                <div className="space-y-2">
                  {ROLE_ORDER.map((rid) => {
                    const Icon = ROLE_ICONS[rid] || Sparkles
                    return (
                      <div key={rid} className="rounded-xl px-3.5 py-2.5 flex items-start gap-3" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
                        <div className="w-8 h-8 rounded-lg flex items-center justify-center shrink-0" style={{ background: 'rgba(240,185,11,0.08)', border: '1px solid rgba(240,185,11,0.25)' }}>
                          <Icon className="w-4 h-4 text-amber-400" />
                        </div>
                        <div className="min-w-0">
                          <div className="text-[12px] font-medium text-[#EAECEF]">{t(`aiCouncil.roles.${rid}`, language)}</div>
                          <div className="text-[10px] text-[#848E9C] leading-relaxed mt-0.5">{t(`aiCouncil.roleIntro.${rid}`, language)}</div>
                        </div>
                      </div>
                    )
                  })}
                </div>
              </div>
            </div>
          </div>
        )}

        {/* ============ 阶段 B：运行 / 完成（实时圆桌）============ */}
        {council && (
          <div className="flex-1 flex min-h-0">
            {/* 左：实时消息流 */}
            <div className="flex-1 flex flex-col min-w-0 border-r border-[#2B3139]">
              <div className="px-4 py-2 border-b border-[#2B3139] shrink-0 flex items-center gap-2">
                <MessageSquare className="w-3.5 h-3.5 text-amber-400" />
                <span className="text-[11px] font-semibold text-[#EAECEF]">{t('aiCouncil.liveFeed', language)}</span>
                {isRunning && <span className="text-[10px] text-[#848E9C]">{t('aiCouncil.running', language)}</span>}
                <span className="ml-auto text-[10px] text-[#848E9C] tabular-nums">{transcript.length} msgs</span>
              </div>
              <div ref={feedRef} className="flex-1 overflow-y-auto px-4 py-3 space-y-2.5">
                {transcript.map((e, i) => (
                  <TranscriptItem key={i} entry={e} lang={language} />
                ))}
                {isRunning && (
                  <div className="flex items-center gap-2 px-2 py-1">
                    <Loader2 className="w-3 h-3 animate-spin text-amber-400" />
                    <span className="text-[10px] text-[#848E9C]">{t('aiCouncil.thinking', language)}</span>
                  </div>
                )}
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
                    {language === 'zh' ? '圆桌已取消。' : 'Council cancelled.'}
                  </div>
                )}
              </div>
            </div>

            {/* 右：角色名册 + 预算 + 结果 */}
            <div className="w-[300px] shrink-0 flex flex-col min-h-0">
              <div className="flex-1 overflow-y-auto px-3.5 py-3 space-y-4">
                {/* 预算进度 */}
                <div className="rounded-xl px-3.5 py-3" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
                  <div className="flex items-center justify-between mb-1.5">
                    <span className="text-[10px] text-[#848E9C]">{t('aiCouncil.budgetLabel', language)}</span>
                    <span className="text-[11px] text-amber-400 font-semibold tabular-nums">
                      {usedBudget}/{totalBudget}
                    </span>
                  </div>
                  <div className="h-1.5 rounded-full overflow-hidden" style={{ background: '#0B0E11' }}>
                    <div
                      className="h-full rounded-full transition-all duration-500"
                      style={{ width: `${budgetPct}%`, background: budgetPct > 85 ? '#F6465D' : 'linear-gradient(90deg, #F0B90B, #F8D33A)' }}
                    />
                  </div>
                </div>

                {/* 角色名册 */}
                <div>
                  <div className="text-[10px] font-semibold text-amber-400/90 uppercase tracking-wider mb-2">{t('aiCouncil.roster', language)}</div>
                  <div className="space-y-1.5">
                    {[...steps]
                      .sort((a, b) => ROLE_ORDER.indexOf(a.role) - ROLE_ORDER.indexOf(b.role))
                      .map((step) => {
                        const Icon = ROLE_ICONS[step.role] || Sparkles
                        const running = step.status === 'running'
                        return (
                          <div
                            key={step.role}
                            className="rounded-lg px-2.5 py-2 flex items-center gap-2.5 transition-all"
                            style={{
                              background: running ? 'rgba(240,185,11,0.07)' : '#1E2329',
                              border: running ? '1px solid rgba(240,185,11,0.45)' : '1px solid #2B3139',
                            }}
                          >
                            <Icon className={`w-3.5 h-3.5 shrink-0 ${running ? 'text-amber-400' : step.status === 'done' ? 'text-[#0ECB81]' : 'text-[#5C6470]'}`} />
                            <span className={`text-[11px] flex-1 min-w-0 truncate ${running ? 'text-amber-400 font-medium' : step.status === 'pending' ? 'text-[#5C6470]' : 'text-[#EAECEF]'}`}>
                              {t(`aiCouncil.roles.${step.role}`, language)}
                            </span>
                            {running && stepStartRef.current.has(step.role) && (
                              <span className="flex items-center gap-0.5 text-[10px] text-amber-400 tabular-nums shrink-0">
                                <Timer className="w-3 h-3" />
                                {Math.max(0, Math.floor((nowTick - (stepStartRef.current.get(step.role) || 0)) / 1000))}s
                              </span>
                            )}
                            {!running && step.duration_ms ? (
                              <span className="text-[10px] text-[#5C6470] tabular-nums shrink-0">{(step.duration_ms / 1000).toFixed(0)}s</span>
                            ) : null}
                            {step.status === 'done' && <CheckCircle2 className="w-3.5 h-3.5 text-[#0ECB81] shrink-0" />}
                            {step.status === 'failed' && <XCircle className="w-3.5 h-3.5 text-[#F6465D] shrink-0" />}
                            {step.status === 'running' && <Loader2 className="w-3.5 h-3.5 animate-spin text-amber-400 shrink-0" />}
                            {step.status === 'pending' && <span className="w-1.5 h-1.5 rounded-full bg-[#2B3139] shrink-0" />}
                            {step.status === 'skipped' && <span className="text-[9px] text-[#5C6470] shrink-0">—</span>}
                            {step.error && <span title={step.error}>⚠️</span>}
                          </div>
                        )
                      })}
                  </div>
                  {doneCount > 0 && (
                    <div className="mt-2 text-[10px] text-[#848E9C] text-center">
                      {doneCount}/{steps.length} {language === 'zh' ? '位专家已发言' : 'experts spoken'}
                    </div>
                  )}
                </div>

                {/* 结果（完成后出现在右栏） */}
                {council.status === 'completed' && council.result && (
                  <div className="rounded-xl p-3.5 space-y-3" style={{ background: '#1E2329', border: '1px solid rgba(240,185,11,0.35)' }}>
                    <div className="flex items-center gap-2">
                      <FileCheck className="w-4 h-4 text-amber-400" />
                      <span className="text-[13px] font-semibold text-[#EAECEF]">{t('aiCouncil.resultTitle', language)}</span>
                    </div>

                    {/* AI 取的策略名（生成模式显眼展示） */}
                    {council.result.strategy_name && (
                      <div className="rounded-lg px-3 py-2.5" style={{ background: 'rgba(240,185,11,0.07)', border: '1px solid rgba(240,185,11,0.35)' }}>
                        <div className="text-[9px] uppercase tracking-wider text-amber-400/70 mb-0.5">{t('aiCouncil.aiNamed', language)}</div>
                        <div className="text-[15px] font-bold text-amber-400 leading-snug break-words">{council.result.strategy_name}</div>
                        {council.result.strategy_description && (
                          <p className="text-[10px] text-[#848E9C] mt-1 leading-relaxed">{council.result.strategy_description}</p>
                        )}
                      </div>
                    )}

                    {council.result.reasoning && (
                      <div>
                        <div className="text-[10px] text-[#848E9C] mb-1">{t('aiCouncil.reasoning', language)}</div>
                        <p className="text-[11px] text-[#B7BDC6] leading-relaxed whitespace-pre-wrap">{council.result.reasoning}</p>
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

                    {council.mode === 'generate' && onCreateStrategy ? (
                      <button
                        onClick={handleAddToList}
                        disabled={applied || creating}
                        className={`w-full py-2 rounded-lg text-[12px] font-medium transition-all flex items-center justify-center gap-1.5 ${
                          applied ? 'bg-[#0ECB81]/15 text-[#0ECB81]' : 'bg-gradient-to-r from-amber-400 to-amber-500 text-[#16181C] hover:opacity-90'
                        }`}
                      >
                        {creating ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : applied ? <CheckCircle2 className="w-3.5 h-3.5" /> : <FileCheck className="w-3.5 h-3.5" />}
                        {applied ? t('aiCouncil.addedToList', language) : t('aiCouncil.addToList', language)}
                      </button>
                    ) : (
                      <button
                        onClick={handleApply}
                        disabled={applied}
                        className={`w-full py-2 rounded-lg text-[12px] font-medium transition-all ${
                          applied ? 'bg-[#0ECB81]/15 text-[#0ECB81]' : 'bg-gradient-to-r from-amber-400 to-amber-500 text-[#16181C] hover:opacity-90'
                        }`}
                      >
                        {applied ? t('aiCouncil.applied', language) : t('aiCouncil.apply', language)}
                      </button>
                    )}
                  </div>
                )}
              </div>

              {/* 右栏底部操作 */}
              <div className="px-3.5 py-3 border-t border-[#2B3139] shrink-0">
                <button
                  onClick={backToForm}
                  className="w-full py-1.5 rounded-lg text-[12px] text-[#848E9C] hover:text-[#EAECEF] transition-colors"
                  style={{ background: '#1E2329', border: '1px solid #2B3139' }}
                >
                  {language === 'zh' ? '返回重新发起' : 'Start New Session'}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
