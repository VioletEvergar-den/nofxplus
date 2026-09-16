import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { motion } from 'framer-motion'
import {
  Sparkles,
  Activity,
  CheckCircle2,
  Circle,
  PlayCircle,
  GitBranch,
  BarChart3,
  Info,
  AlertTriangle,
  Loader2,
} from 'lucide-react'
import { api } from '../lib/api'
import { useLanguage } from '../contexts/LanguageContext'

export interface PromptVariant {
  id: string
  promptRoleDefinition: string
  promptTradingFrequency: string
  promptEntryStandards: string
  promptDecisionProcess: string
  createdAt: string
  totalDecisions: number
  totalReturn: number
  winRate: number
  profitFactor: number
  sharpeRatio: number
  maxDrawdown: number
  fitnessScore: number
  generation: number
  isActive: boolean
}

export interface PromptVariantLabResponse {
  variants: PromptVariant[]
  total: number
  generation: number
  active: PromptVariant
  timestamp: string
  message?: string
  error?: string
}

export interface PromptVariantPerformanceResponse {
  variant: PromptVariant
  timestamp: string
  message?: string
}

const toNumber = (value: unknown) => {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : 0
  }
  return 0
}

const normalizeVariant = (variant: Partial<PromptVariant> | null | undefined): PromptVariant => {
  const v = variant ?? {}
  return {
    id: typeof v.id === 'string' ? v.id : '',
    promptRoleDefinition: typeof v.promptRoleDefinition === 'string' ? v.promptRoleDefinition : '',
    promptTradingFrequency: typeof v.promptTradingFrequency === 'string' ? v.promptTradingFrequency : '',
    promptEntryStandards: typeof v.promptEntryStandards === 'string' ? v.promptEntryStandards : '',
    promptDecisionProcess: typeof v.promptDecisionProcess === 'string' ? v.promptDecisionProcess : '',
    createdAt: typeof v.createdAt === 'string' ? v.createdAt : '',
    totalDecisions: toNumber(v.totalDecisions),
    totalReturn: toNumber(v.totalReturn),
    winRate: toNumber(v.winRate),
    profitFactor: toNumber(v.profitFactor),
    sharpeRatio: toNumber(v.sharpeRatio),
    maxDrawdown: toNumber(v.maxDrawdown),
    fitnessScore: toNumber(v.fitnessScore),
    generation: Math.trunc(toNumber(v.generation)),
    isActive: Boolean(v.isActive),
  }
}

interface PromptVariantLabProps {
  type: 'backtest' | 'trader'
  resourceId?: string
  onBack?: () => void
  /** 是否渲染内部大标题（嵌入弹窗且外壳已有标题时传 false） */
  showHeader?: boolean
}

export function PromptVariantLab({ type, resourceId, showHeader = true }: PromptVariantLabProps) {
  const { language } = useLanguage()
  const [selectedVariant, setSelectedVariant] = useState<PromptVariant | null>(null)
  const [activating, setActivating] = useState<string | null>(null)
  const [lastNonEmptyVariants, setLastNonEmptyVariants] = useState<PromptVariant[]>([])

  const isBacktest = type === 'backtest'
  const isTrader = type === 'trader'

  // Load cached variants from sessionStorage on mount or resourceId change (backtest only)
  useEffect(() => {
    if (isBacktest && resourceId) {
      const cached = sessionStorage.getItem(`promptlab-variants-${resourceId}`)
      if (cached) {
        try {
          const parsed = JSON.parse(cached)
          if (Array.isArray(parsed) && parsed.length > 0) {
            setLastNonEmptyVariants(parsed.map((variant) => normalizeVariant(variant)))
          }
        } catch {}
      }
    }
  }, [resourceId, isBacktest])

  // Fetch prompt variants based on type
  const shouldFetch = !!resourceId
  const fetchPromptVariants = async (id: string) => {
    try {
      if (isBacktest) {
        return await api.getPromptVariants(id)
      } else if (isTrader) {
        return await api.getTraderPromptVariants(id)
      }
    } catch (err) {
      console.error(`[PromptVariantLab] Fetch error (${type}):`, err)
      throw err
    }
  }

  const { data, error, mutate } = useSWR(
    shouldFetch ? `prompt-variants-${type}-${resourceId}` : null,
    shouldFetch ? () => fetchPromptVariants(resourceId!) : null,
    {
      refreshInterval: 10000,
      onError: (err) => {
        console.error(`[PromptVariantLab] SWR Error (${type}):`, err)
      },
      revalidateOnFocus: true,
      revalidateOnMount: true,
      revalidateIfStale: true,
      revalidateOnReconnect: true,
      dedupingInterval: 0,
    }
  )

  // Fetch performance data (both backtest and trader)
  const fetchPerformance = async (id: string) => {
    try {
      if (isBacktest) {
        // Note: backtest doesn't have separate performance endpoint, uses variants data
        return null
      } else if (isTrader) {
        return await api.getTraderPromptPerformance(id)
      }
    } catch (err) {
      console.error(`[PromptVariantLab] Performance fetch error:`, err)
      throw err
    }
  }

  const { data: performanceData, error: performanceError } = useSWR(
    shouldFetch && isTrader ? `prompt-performance-${type}-${resourceId}` : null,
    shouldFetch && isTrader ? () => fetchPerformance(resourceId!) : null,
    { refreshInterval: 10000 }
  )


  // Save variants to sessionStorage when fetched (backtest only)
  useEffect(() => {
    if (isBacktest && data?.variants && data.variants.length > 0 && resourceId) {
      const normalized = data.variants.map((variant) => normalizeVariant(variant))
      setLastNonEmptyVariants(normalized)
      sessionStorage.setItem(`promptlab-variants-${resourceId}`, JSON.stringify(normalized))
    }
  }, [data?.variants, resourceId, isBacktest])

  const variants = Array.isArray(data?.variants)
    ? data.variants.map((variant) => normalizeVariant(variant)).filter((v) => v.id)
    : []
  const showVariants = isBacktest && variants.length === 0 ? lastNonEmptyVariants : variants
  const everHadVariants = isBacktest ? lastNonEmptyVariants.length > 0 : true
  const activeVariant = showVariants.length > 0 ? showVariants.find((v) => v.isActive) : undefined
  const performanceVariant = performanceData?.variant
    ? normalizeVariant(performanceData.variant)
    : undefined

  useEffect(() => {
    if (activeVariant && !selectedVariant) {
      setSelectedVariant(activeVariant)
    }
  }, [activeVariant, selectedVariant])

  const handleActivate = async (variantId: string) => {
    if (!resourceId || activating) return

    setActivating(variantId)
    try {
      if (isBacktest) {
        await api.activatePromptVariant(resourceId, variantId)
      } else if (isTrader) {
        await api.activateTraderPromptVariant(resourceId, variantId)
      }
      mutate()
    } catch (err: any) {
      console.error(`Failed to activate variant (${type}):`, err)
    } finally {
      setActivating(null)
    }
  }

  const getGenerationColor = (generation: number) => {
    const colors = [
      'text-blue-400',
      'text-purple-400',
      'text-pink-400',
      'text-orange-400',
      'text-[#0ECB81]',
    ]
    return colors[generation % colors.length]
  }

  const getFitnessColor = (score: number) => {
    if (score >= 0.8) return 'text-[#0ECB81]'
    if (score >= 0.5) return 'text-[#F0B90B]'
    return 'text-[#F6465D]'
  }

  if (!resourceId) {
    return (
      <div className="flex items-center justify-center h-96">
        <div className="text-center">
          <Info className="h-12 w-12 mx-auto mb-4 text-[#5E6673]" />
          <p className="text-[#848E9C]">
            {isBacktest
              ? language === 'zh'
                ? '请选择一个回测查看提示词优化'
                : 'Select a backtest to view prompt optimization'
              : language === 'zh'
              ? '请选择一个交易员查看提示词优化'
              : 'Select a trader to view prompt optimization'}
          </p>
        </div>
      </div>
    )
  }

  if (error && !everHadVariants) {
    console.error(`[PromptVariantLab] Error loading (${type}):`, error)
    return (
      <div className="flex items-center justify-center h-96">
        <div className="text-center">
          <AlertTriangle className="h-12 w-12 mx-auto mb-4 text-[#F6465D]" />
          <p className="text-[#F6465D]">
            {language === 'zh'
              ? '加载提示词优化数据失败'
              : 'Failed to load prompt optimization data'}
          </p>
          <p className="text-[#5E6673] text-sm mt-2">{String(error)}</p>
        </div>
      </div>
    )
  }

  if (!data && !everHadVariants) {
    return (
      <div className="flex items-center justify-center h-96">
        <div className="text-center">
          <Loader2 className="h-12 w-12 mx-auto mb-4 text-[#F0B90B] animate-spin" />
          <p className="text-[#848E9C]">
            {language === 'zh' ? '加载中...' : 'Loading...'}
          </p>
        </div>
      </div>
    )
  }

  if (showVariants.length === 0 && !everHadVariants) {
    return (
      <div className="flex items-center justify-center h-96">
        <div className="text-center">
          <Activity className="h-12 w-12 mx-auto mb-3 text-[#4A5158]" />
          <p className="text-[#848E9C]">
            {language === 'zh'
              ? '提示词优化未启用或暂无变体'
              : 'Prompt optimization not enabled or no variants yet'}
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      {showHeader && (
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Sparkles className="h-6 w-6 text-[#F0B90B]" />
            <h2 className="text-2xl font-bold text-[#EAECEF]">
              {isBacktest
                ? language === 'zh'
                  ? '提示词实验室'
                  : 'Prompt Lab'
                : language === 'zh'
                ? '交易员提示词实验室'
                : 'Trader Prompt Lab'}
            </h2>
          </div>
          <div className="text-sm text-[#848E9C]">
            {language === 'zh' ? '代 #' : 'Gen #'}
            <span className={`ml-2 font-bold ${getGenerationColor(data?.generation ?? 0)}`}>
              {data?.generation ?? 0}
            </span>
          </div>
        </div>
      )}

      {/* Stats Summary */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div className="bg-[#1E2329] rounded-xl p-4 border border-[#2B3139]">
          <div className="text-[#848E9C] text-xs mb-1">
            {language === 'zh' ? '总变体数' : 'Total Variants'}
          </div>
          <div className="text-2xl font-bold text-[#EAECEF]">{showVariants.length}</div>
        </div>
        <div className="bg-[#1E2329] rounded-xl p-4 border border-[#2B3139]">
          <div className="text-[#848E9C] text-xs mb-1">
            {language === 'zh' ? '总代数' : 'Generations'}
          </div>
          <div className="text-2xl font-bold text-[#F0B90B]">{data?.generation ?? 0}</div>
        </div>
        <div className="bg-[#1E2329] rounded-xl p-4 border border-[#2B3139]">
          <div className="text-[#848E9C] text-xs mb-1">
            {language === 'zh' ? '活跃变体' : 'Active Variant'}
          </div>
          <div className="text-2xl font-bold text-[#0ECB81]">
            {activeVariant ? `Gen ${activeVariant.generation}` : '-'}
          </div>
        </div>
        <div className="bg-[#1E2329] rounded-xl p-4 border border-[#2B3139]">
          <div className="text-[#848E9C] text-xs mb-1">
            {language === 'zh' ? '活跃适应度' : 'Active Fitness'}
          </div>
          <div className={`text-2xl font-bold ${getFitnessColor(activeVariant?.fitnessScore ?? 0)}`}>
            {activeVariant ? ((activeVariant.fitnessScore ?? 0) * 100).toFixed(0) : '-'}%
          </div>
        </div>
      </div>

      {/* Variants Grid */}
      <div>
        <h3 className="text-lg font-semibold text-[#EAECEF] mb-4">
          {language === 'zh' ? '提示词变体' : 'Prompt Variants'}
        </h3>

        {showVariants.length === 0 ? (
          <div className="bg-[#1E2329]/60 rounded-xl border border-dashed border-[#2B3139] p-8 text-center">
            <Activity className="h-12 w-12 mx-auto mb-3 text-[#4A5158]" />
            <p className="text-[#848E9C]">
              {language === 'zh'
                ? '等待提示词优化生成第一个变体...'
                : 'Waiting for prompt optimization to generate the first variant...'}
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {showVariants.map((variant, idx) => (
              <motion.div
                key={variant.id}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: idx * 0.05 }}
                onClick={() => setSelectedVariant(variant)}
                className={`cursor-pointer rounded-xl border transition-all ${
                  variant.isActive
                    ? 'border-[#0ECB81]/50 bg-[#0ECB81]/10 ring-2 ring-[#0ECB81]/30'
                    : selectedVariant?.id === variant.id
                    ? 'border-[#F0B90B]/60 bg-[#F0B90B]/10'
                    : 'border-[#2B3139] bg-[#1E2329]/60 hover:border-[#3A4149]'
                } p-4`}
              >
                {/* Header */}
                <div className="flex items-start justify-between mb-3">
                  <div className="flex items-center gap-2">
                    {variant.isActive ? (
                      <CheckCircle2 className="h-5 w-5 text-[#0ECB81]" />
                    ) : (
                      <Circle className="h-5 w-5 text-[#5E6673]" />
                    )}
                    <div>
                      <div className={`font-semibold text-sm ${getGenerationColor(variant.generation)}`}>
                        Gen {variant.generation}
                      </div>
                      <div className="text-xs text-[#848E9C]">
                        {variant.createdAt ? new Date(variant.createdAt).toLocaleTimeString() : '-'}
                      </div>
                    </div>
                  </div>

                  {variant.isActive && (
                    <div className="flex items-center gap-1 px-2 py-1 bg-[#0ECB81]/20 rounded-lg text-xs text-[#0ECB81]">
                      <Activity className="h-3 w-3" />
                      {language === 'zh' ? '活跃' : 'Active'}
                    </div>
                  )}
                </div>

                {/* Performance Metrics */}
                <div className="space-y-2 mb-4 pb-4 border-b border-[#2B3139]">
                  <div className="flex justify-between items-center text-sm">
                    <span className="text-[#848E9C]">
                      {language === 'zh' ? '总决策' : 'Decisions'}
                    </span>
                    <span className="text-[#EAECEF] font-medium">{variant.totalDecisions}</span>
                  </div>
                  <div className="flex justify-between items-center text-sm">
                    <span className="text-[#848E9C]">
                      {language === 'zh' ? '总收益率' : 'Return'}
                    </span>
                    <span className={variant.totalReturn >= 0 ? 'text-[#0ECB81]' : 'text-[#F6465D]'}>
                      {variant.totalReturn.toFixed(2)}%
                    </span>
                  </div>
                  <div className="flex justify-between items-center text-sm">
                    <span className="text-[#848E9C]">
                      {language === 'zh' ? '胜率' : 'Win Rate'}
                    </span>
                    <span className="text-[#EAECEF]">{variant.winRate.toFixed(1)}%</span>
                  </div>
                  <div className="flex justify-between items-center text-sm">
                    <span className="text-[#848E9C]">
                      {language === 'zh' ? '利润因子' : 'Profit Factor'}
                    </span>
                    <span className="text-[#EAECEF]">{variant.profitFactor.toFixed(2)}</span>
                  </div>
                </div>

                {/* Fitness Score */}
                <div className="flex items-center justify-between mb-4">
                  <span className="text-sm text-[#848E9C]">
                    {language === 'zh' ? '适应度分数' : 'Fitness Score'}
                  </span>
                  <div className={`text-lg font-bold ${getFitnessColor(variant.fitnessScore)}`}>
                    {(variant.fitnessScore * 100).toFixed(1)}%
                  </div>
                </div>

                {/* Activate Button */}
                {!variant.isActive && (
                  <button
                    onClick={() => handleActivate(variant.id)}
                    disabled={activating !== null}
                    className="w-full py-2 px-3 bg-[#F0B90B] hover:bg-[#D4A70A] disabled:bg-[#2B3139] disabled:text-[#5E6673] text-[#111418] text-sm font-semibold rounded-lg transition-colors flex items-center justify-center gap-2"
                  >
                    {activating === variant.id ? (
                      <>
                        <Loader2 className="h-4 w-4 animate-spin" />
                        {language === 'zh' ? '激活中...' : 'Activating...'}
                      </>
                    ) : (
                      <>
                        <PlayCircle className="h-4 w-4" />
                        {language === 'zh' ? '激活此变体' : 'Activate'}
                      </>
                    )}
                  </button>
                )}
              </motion.div>
            ))}
          </div>
        )}
      </div>

      {/* Selected Variant Details */}
      {selectedVariant && (
        <div className="bg-[#1E2329] rounded-xl border border-[#2B3139] p-6">
          <div className="flex items-center gap-2 mb-4">
            <GitBranch className="h-5 w-5 text-[#F0B90B]" />
            <h3 className="text-lg font-semibold text-[#EAECEF]">
              {language === 'zh' ? '选中变体详情' : 'Selected Variant Details'}
            </h3>
            {selectedVariant.isActive && (
              <span className="ml-auto text-xs px-2 py-1 bg-[#0ECB81]/20 text-[#0ECB81] rounded-lg">
                {language === 'zh' ? '活跃' : 'Active'}
              </span>
            )}
          </div>

          <div className="grid grid-cols-2 md:grid-cols-3 gap-4 mb-4">
            <div>
              <div className="text-xs text-[#848E9C] mb-1">
                {language === 'zh' ? '代数' : 'Generation'}
              </div>
              <div className={`text-xl font-bold ${getGenerationColor(selectedVariant.generation)}`}>
                {selectedVariant.generation}
              </div>
            </div>
            <div>
              <div className="text-xs text-[#848E9C] mb-1">
                {language === 'zh' ? '创建时间' : 'Created'}
              </div>
              <div className="text-sm text-[#EAECEF]">
                {selectedVariant.createdAt ? new Date(selectedVariant.createdAt).toLocaleString() : '-'}
              </div>
            </div>
            <div>
              <div className="text-xs text-[#848E9C] mb-1">
                {language === 'zh' ? '适应度分数' : 'Fitness'}
              </div>
              <div className={`text-xl font-bold ${getFitnessColor(selectedVariant.fitnessScore)}`}>
                {(selectedVariant.fitnessScore * 100).toFixed(1)}%
              </div>
            </div>
          </div>

          {/* Prompt Text */}
          <div>
            <div className="text-sm text-[#848E9C] mb-2">
              {language === 'zh' ? '系统提示词' : 'System Prompt'}
            </div>
            <div className="bg-[#0B0E11] rounded-lg p-3 border border-[#2B3139] max-h-48 overflow-y-auto space-y-4">
              <div>
                <div className="font-bold text-blue-400 mb-1">
                  {language === 'zh' ? '角色定义' : 'Role Definition'}
                </div>
                <p className="text-sm text-[#B7BDC6] whitespace-pre-wrap font-mono">
                  {selectedVariant.promptRoleDefinition || (language === 'zh' ? '无' : 'N/A')}
                </p>
              </div>
              <div>
                <div className="font-bold text-purple-400 mb-1">
                  {language === 'zh' ? '交易频率' : 'Trading Frequency'}
                </div>
                <p className="text-sm text-[#B7BDC6] whitespace-pre-wrap font-mono">
                  {selectedVariant.promptTradingFrequency || (language === 'zh' ? '无' : 'N/A')}
                </p>
              </div>
              <div>
                <div className="font-bold text-pink-400 mb-1">
                  {language === 'zh' ? '入场标准' : 'Entry Standards'}
                </div>
                <p className="text-sm text-[#B7BDC6] whitespace-pre-wrap font-mono">
                  {selectedVariant.promptEntryStandards || (language === 'zh' ? '无' : 'N/A')}
                </p>
              </div>
              <div>
                <div className="font-bold text-orange-400 mb-1">
                  {language === 'zh' ? '决策流程' : 'Decision Process'}
                </div>
                <p className="text-sm text-[#B7BDC6] whitespace-pre-wrap font-mono">
                  {selectedVariant.promptDecisionProcess || (language === 'zh' ? '无' : 'N/A')}
                </p>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Prompt Variant Performance (Trader only) */}
      {isTrader && (
        <div>
          <h3 className="text-lg font-semibold text-[#EAECEF] mb-4 flex items-center gap-2">
            <BarChart3 className="h-5 w-5 text-[#F0B90B]" />
            {language === 'zh' ? '提示词变体表现' : 'Prompt Variant Performance'}
          </h3>
          {performanceError && (
            <div className="text-[#F6465D] text-sm mb-2">
              {language === 'zh' ? '加载表现数据失败' : 'Failed to load performance data'}
            </div>
          )}
          {!performanceData ? (
            <div className="flex items-center gap-2 text-[#848E9C]">
              <Loader2 className="h-4 w-4 animate-spin" />
              {language === 'zh' ? '加载中...' : 'Loading...'}
            </div>
          ) : (
            <div className="bg-[#1E2329] rounded-xl border border-[#2B3139] p-4 mb-6">
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div>
                  <div className="text-xs text-[#848E9C] mb-1">
                    {language === 'zh' ? '总收益率' : 'Total Return'}
                  </div>
                  <div className="text-xl font-bold text-[#0ECB81]">
                    {(performanceVariant?.totalReturn ?? 0).toFixed(2)}%
                  </div>
                </div>
                <div>
                  <div className="text-xs text-[#848E9C] mb-1">
                    {language === 'zh' ? '胜率' : 'Win Rate'}
                  </div>
                  <div className="text-xl font-bold text-[#F0B90B]">
                    {(performanceVariant?.winRate ?? 0).toFixed(1)}%
                  </div>
                </div>
                <div>
                  <div className="text-xs text-[#848E9C] mb-1">
                    {language === 'zh' ? '最大回撤' : 'Max Drawdown'}
                  </div>
                  <div className="text-xl font-bold text-[#F6465D]">
                    {(performanceVariant?.maxDrawdown ?? 0).toFixed(1)}%
                  </div>
                </div>
                <div>
                  <div className="text-xs text-[#848E9C] mb-1">
                    {language === 'zh' ? '夏普比率' : 'Sharpe Ratio'}
                  </div>
                  <div className="text-xl font-bold text-[#F0B90B]">
                    {(performanceVariant?.sharpeRatio ?? 0).toFixed(2)}
                  </div>
                </div>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Last Updated */}
      <div className="text-xs text-[#5E6673] text-center">
        {language === 'zh' ? '最后更新' : 'Last updated'}:{' '}
        {data?.timestamp ? new Date(data.timestamp).toLocaleTimeString() : '-'}
      </div>
    </div>
  )
}
