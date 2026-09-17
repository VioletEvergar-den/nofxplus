import { useState, useEffect } from 'react'
import type { AIModel, Exchange, CreateTraderRequest, Strategy } from '../types'
import { useLanguage } from '../contexts/LanguageContext'
import { t } from '../i18n/translations'
import { toast } from 'sonner'
import { Pencil, Plus, X as IconX, Sparkles, ExternalLink, UserPlus } from 'lucide-react'
import { httpClient } from '../lib/httpClient'

// 提取下划线后面的名称部分
function getShortName(fullName: string): string {
  const parts = fullName.split('_')
  return parts.length > 1 ? parts[parts.length - 1] : fullName
}

// 交易所注册链接配置
const EXCHANGE_REGISTRATION_LINKS: Record<string, { url: string; hasReferral?: boolean }> = {
  binance: { url: 'https://www.binance.com/join?ref=NOFXENG', hasReferral: true },
  okx: { url: 'https://www.okx.com/join/1865360', hasReferral: true },
  bybit: { url: 'https://partner.bybit.com/b/83856', hasReferral: true },
  hyperliquid: { url: 'https://app.hyperliquid.xyz/join/AITRADING', hasReferral: true },
  aster: { url: 'https://www.asterdex.com/en/referral/fdfc0e', hasReferral: true },
  lighter: { url: 'https://app.lighter.xyz/?referral=68151432', hasReferral: true },
}

import type { TraderConfigData } from '../types'

// 表单内部状态类型
interface FormState {
  trader_id?: string
  trader_name: string
  ai_model: string
  exchange_id: string
  paper_trading: boolean
  strategy_id: string
  trading_mode: string
  is_cross_margin: boolean
  show_in_competition: boolean
  scan_interval_minutes: number
  initial_balance?: number
  enable_feedback: boolean
  enable_llm_feedback: boolean
  enable_prompt_evolution: boolean
  adaptive_interval: boolean
  giveback_mode: string
  giveback_hard_pct: number
}

interface TraderConfigModalProps {
  isOpen: boolean
  onClose: () => void
  traderData?: TraderConfigData | null
  isEditMode?: boolean
  availableModels?: AIModel[]
  availableExchanges?: Exchange[]
  onSave?: (data: CreateTraderRequest) => Promise<void>
}

export function TraderConfigModal({
  isOpen,
  onClose,
  traderData,
  isEditMode = false,
  availableModels = [],
  availableExchanges = [],
  onSave,
}: TraderConfigModalProps) {
  const { language } = useLanguage()
  const [formData, setFormData] = useState<FormState>({
    trader_name: '',
    ai_model: '',
    exchange_id: '',
    paper_trading: false,
    initial_balance: 1000,
    strategy_id: '',
    trading_mode: '',
    is_cross_margin: true,
    show_in_competition: true,
    scan_interval_minutes: 3,
    enable_feedback: true,
    enable_llm_feedback: true,
    enable_prompt_evolution: true,
    adaptive_interval: false,
    giveback_mode: 'off',
    giveback_hard_pct: 30,
  })
  const [isSaving, setIsSaving] = useState(false)
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [isFetchingBalance, setIsFetchingBalance] = useState(false)
  const [balanceFetchError, setBalanceFetchError] = useState<string>('')

  // 获取用户的策略列表
  useEffect(() => {
    const fetchStrategies = async () => {
      try {
        const result = await httpClient.get<{ strategies: Strategy[] }>('/api/strategies')
        if (result.success && result.data?.strategies) {
          const strategyList = result.data.strategies
          setStrategies(strategyList)
          // 如果没有选择策略，默认选中第一个
          if (!formData.strategy_id && !isEditMode) {
            if (strategyList.length > 0) {
              setFormData(prev => ({ ...prev, strategy_id: strategyList[0].id }))
            }
          }
        }
      } catch (error) {
        console.error('Failed to fetch strategies:', error)
      }
    }
    if (isOpen) {
      fetchStrategies()
    }
  }, [isOpen])

  useEffect(() => {
    if (traderData) {
      setFormData({
        ...traderData,
        paper_trading: traderData.paper_trading ?? false,
        strategy_id: traderData.strategy_id || '',
        trading_mode: traderData.trading_mode || '',
        enable_feedback: traderData.enable_feedback ?? true,
        enable_llm_feedback: traderData.enable_llm_feedback ?? true,
        enable_prompt_evolution: traderData.enable_prompt_evolution ?? true,
        adaptive_interval: traderData.adaptive_interval ?? false,
        giveback_mode: traderData.giveback_mode || 'off',
        giveback_hard_pct: traderData.giveback_hard_pct ?? 30,
      })
    } else if (!isEditMode) {
      setFormData({
        trader_name: '',
        ai_model: availableModels[0]?.id || '',
        exchange_id: availableExchanges[0]?.id || '',
        paper_trading: false,
        initial_balance: 1000,
        strategy_id: '',
        trading_mode: '',
        is_cross_margin: true,
        show_in_competition: true,
        scan_interval_minutes: 3,
        enable_feedback: true,
        enable_llm_feedback: true,
        enable_prompt_evolution: true,
        adaptive_interval: false,
        giveback_mode: 'off',
        giveback_hard_pct: 30,
      })
    }
  }, [traderData, isEditMode, availableModels, availableExchanges])

  if (!isOpen) return null

  const handleInputChange = (field: keyof FormState, value: any) => {
    setFormData((prev) => ({ ...prev, [field]: value }))
  }

  const handleFetchCurrentBalance = async () => {
    if (!isEditMode || !traderData?.trader_id) {
      setBalanceFetchError('只有在编辑模式下才能获取当前余额')
      return
    }

    setIsFetchingBalance(true)
    setBalanceFetchError('')

    try {
      const result = await httpClient.get<{
        total_equity?: number
        balance?: number
      }>(`/api/account?trader_id=${traderData.trader_id}`)

      if (result.success && result.data) {
        const currentBalance =
          result.data.total_equity || result.data.balance || 0
        setFormData((prev) => ({ ...prev, initial_balance: currentBalance }))
        toast.success('已获取当前余额')
      } else {
        throw new Error(result.message || '获取余额失败')
      }
    } catch (error) {
      console.error('获取余额失败:', error)
      setBalanceFetchError('获取余额失败，请检查网络连接')
    } finally {
      setIsFetchingBalance(false)
    }
  }

  const handleSave = async () => {
    if (!onSave) return

    setIsSaving(true)
    try {
      const saveData: CreateTraderRequest = {
        name: formData.trader_name,
        ai_model_id: formData.ai_model,
        exchange_id: formData.exchange_id,
        paper_trading: formData.paper_trading,
        strategy_id: formData.strategy_id,
        trading_mode: formData.trading_mode || '',
        is_cross_margin: formData.is_cross_margin,
        show_in_competition: formData.show_in_competition,
        scan_interval_minutes: formData.scan_interval_minutes,
        enable_feedback: formData.enable_feedback,
        enable_llm_feedback: formData.enable_llm_feedback,
        enable_prompt_evolution: formData.enable_prompt_evolution,
        adaptive_interval: formData.adaptive_interval,
        giveback_mode: formData.giveback_mode,
        giveback_hard_pct: formData.giveback_hard_pct,
      }

      // 模拟盘创建时必须传初始资金；编辑模式时可手动更新初始余额
      if (formData.paper_trading && !isEditMode) {
        saveData.initial_balance = formData.initial_balance || 1000
      } else if (isEditMode && formData.initial_balance !== undefined) {
        saveData.initial_balance = formData.initial_balance
      }

      await toast.promise(onSave(saveData), {
        loading: '正在保存…',
        success: '保存成功',
        error: '保存失败',
      })
      onClose()
    } catch (error) {
      console.error('保存失败:', error)
    } finally {
      setIsSaving(false)
    }
  }

  const selectedStrategy = strategies.find(s => s.id === formData.strategy_id)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-50 backdrop-blur-sm p-4 overflow-y-auto">
      <div
        className="bg-[#1E2329] border border-[#2B3139] rounded-xl shadow-2xl max-w-2xl w-full my-8"
        style={{ maxHeight: 'calc(100vh - 4rem)' }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between p-6 border-b border-[#2B3139] bg-gradient-to-r from-[#1E2329] to-[#252B35] sticky top-0 z-10 rounded-t-xl">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-[#F0B90B] to-[#E1A706] flex items-center justify-center text-black">
              {isEditMode ? (
                <Pencil className="w-5 h-5" />
              ) : (
                <Plus className="w-5 h-5" />
              )}
            </div>
            <div>
              <h2 className="text-xl font-bold text-[#EAECEF]">
                {isEditMode ? '修改交易员' : '创建交易员'}
              </h2>
              <p className="text-sm text-[#848E9C] mt-1">
                {isEditMode ? '修改交易员配置' : '选择策略并配置基础参数'}
              </p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="w-8 h-8 rounded-lg text-[#848E9C] hover:text-[#EAECEF] hover:bg-[#2B3139] transition-colors flex items-center justify-center"
          >
            <IconX className="w-4 h-4" />
          </button>
        </div>

        {/* Content */}
        <div
          className="p-6 space-y-6 overflow-y-auto"
          style={{ maxHeight: 'calc(100vh - 16rem)' }}
        >
          {/* Basic Info */}
          <div className="bg-[#0B0E11] border border-[#2B3139] rounded-lg p-5">
            <h3 className="text-lg font-semibold text-[#EAECEF] mb-5 flex items-center gap-2">
              <span className="text-[#F0B90B]">1</span> 基础配置
            </h3>
            <div className="space-y-4">
              <div>
                <label className="text-sm text-[#EAECEF] block mb-2">
                  交易员名称 <span className="text-red-500">*</span>
                </label>
                <input
                  type="text"
                  value={formData.trader_name}
                  onChange={(e) =>
                    handleInputChange('trader_name', e.target.value)
                  }
                  className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
                  placeholder="请输入交易员名称"
                />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="text-sm text-[#EAECEF] block mb-2">
                    AI模型 <span className="text-red-500">*</span>
                  </label>
                  <select
                    value={formData.ai_model}
                    onChange={(e) =>
                      handleInputChange('ai_model', e.target.value)
                    }
                    className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
                  >
                    {availableModels.map((model) => (
                      <option key={model.id} value={model.id}>
                        {getShortName(model.name || model.id).toUpperCase()}
                      </option>
                    ))}
                  </select>
                </div>
                {/* 模拟盘模式开关（仅创建模式，创建后不可更改） */}
                {!isEditMode && (
                  <div>
                    <label className="flex items-center gap-2 cursor-pointer mb-2">
                      <input
                        type="checkbox"
                        checked={formData.paper_trading}
                        onChange={(e) =>
                          handleInputChange('paper_trading', e.target.checked)
                        }
                        className="accent-[#F0B90B]"
                      />
                      <span className="text-sm text-[#EAECEF]">
                        🎮 本地模拟盘（不需要交易所账号，用币安真实行情模拟成交）
                      </span>
                    </label>
                    <p className="text-xs text-[#848E9C] mb-3">
                      {language === 'zh'
                        ? '开启后 AI 交易员在本地模拟环境运行，资金为虚拟资金，可自定义初始金额'
                        : 'When enabled, the AI trader runs in a local simulated environment with virtual funds'}
                    </p>
                  </div>
                )}
                {!formData.paper_trading && (
                  <div>
                    <label className="text-sm text-[#EAECEF] block mb-2">
                      交易所{' '}
                      {!formData.paper_trading && (
                        <span className="text-red-500">*</span>
                      )}
                    </label>
                    <select
                      value={formData.exchange_id}
                      onChange={(e) =>
                        handleInputChange('exchange_id', e.target.value)
                      }
                      className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
                    >
                      {availableExchanges.map((exchange) => (
                        <option key={exchange.id} value={exchange.id}>
                          {getShortName(exchange.name || exchange.exchange_type || exchange.id).toUpperCase()}
                          {exchange.account_name ? ` - ${exchange.account_name}` : ''}
                        </option>
                      ))}
                    </select>
                    {/* Exchange Registration Link */}
                    {formData.exchange_id && (() => {
                      // Find the selected exchange to get its type
                      const selectedExchange = availableExchanges.find(e => e.id === formData.exchange_id)
                      const exchangeType = selectedExchange?.exchange_type?.toLowerCase() || ''
                      const regLink = EXCHANGE_REGISTRATION_LINKS[exchangeType]
                      if (!regLink) return null
                      return (
                        <a
                          href={regLink.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="mt-2 inline-flex items-center gap-1.5 text-xs text-[#848E9C] hover:text-[#F0B90B] transition-colors"
                        >
                          <UserPlus className="w-3.5 h-3.5" />
                          <span>还没有交易所账号？点击注册</span>
                          {regLink.hasReferral && (
                            <span className="px-1.5 py-0.5 bg-[#F0B90B]/10 text-[#F0B90B] rounded text-[10px]">
                              折扣优惠
                            </span>
                          )}
                          <ExternalLink className="w-3 h-3" />
                        </a>
                      )
                    })()}
                  </div>
                )}
                {formData.paper_trading && (
                  <div>
                    <label className="text-sm text-[#EAECEF] block mb-2">
                      初始资金 (USDT) <span className="text-red-500">*</span>
                    </label>
                    <input
                      type="number"
                      value={formData.initial_balance ?? 1000}
                      onChange={(e) =>
                        handleInputChange('initial_balance', Number(e.target.value))
                      }
                      className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
                      min="1"
                      step="0.01"
                    />
                    <p className="text-xs text-[#848E9C] mt-1">
                      模拟账户的虚拟起始资金，支持任意金额（如 100 / 1000 / 10000）
                    </p>
                  </div>
                )}
              </div>
            </div>
          </div>

          {/* Strategy Selection */}
          <div className="bg-[#0B0E11] border border-[#2B3139] rounded-lg p-5">
            <h3 className="text-lg font-semibold text-[#EAECEF] mb-5 flex items-center gap-2">
              <span className="text-[#F0B90B]">2</span> 选择交易策略
              <Sparkles className="w-4 h-4 text-[#F0B90B]" />
            </h3>
            <div className="space-y-4">
              <div>
                <label className="text-sm text-[#EAECEF] block mb-2">
                  使用策略
                </label>
                <select
                  value={formData.strategy_id}
                  onChange={(e) =>
                    handleInputChange('strategy_id', e.target.value)
                  }
                  className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
                >
                  <option value="">-- 不使用策略（手动配置）--</option>
                  {strategies.map((strategy) => (
                    <option key={strategy.id} value={strategy.id}>
                      {strategy.name}
                      {strategy.is_default ? ' [默认]' : ''}
                    </option>
                  ))}
                </select>
                {strategies.length === 0 && (
                  <p className="text-xs text-[#848E9C] mt-2">
                    暂无策略，请先在策略工作室创建策略
                  </p>
                )}
              </div>

              {/* Strategy Preview */}
              {selectedStrategy && (
                <div className="mt-3 p-4 bg-[#1E2329] border border-[#2B3139] rounded-lg">
                  <div className="flex items-center gap-2 mb-2">
                    <span className="text-[#F0B90B] text-sm font-medium">
                      策略详情
                    </span>
                  </div>
                  <p className="text-sm text-[#848E9C] mb-2">
                    {selectedStrategy.description || '无描述'}
                  </p>
                  <div className="grid grid-cols-2 gap-2 text-xs text-[#848E9C]">
                    <div>
                      币种来源: {selectedStrategy.config.coin_source.source_type === 'static' ? '固定币种' :
                        selectedStrategy.config.coin_source.source_type === 'coinpool' ? 'Coin Pool' :
                        selectedStrategy.config.coin_source.source_type === 'oi_top' ? 'OI Top' : '混合'}
                    </div>
                    <div>
                      保证金上限: {((selectedStrategy.config.risk_control?.max_margin_usage || 0.9) * 100).toFixed(0)}%
                    </div>
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* Trading Parameters */}
          <div className="bg-[#0B0E11] border border-[#2B3139] rounded-lg p-5">
            <h3 className="text-lg font-semibold text-[#EAECEF] mb-5 flex items-center gap-2">
              <span className="text-[#F0B90B]">3</span> 交易参数
            </h3>
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="text-sm text-[#EAECEF] block mb-2">
                    保证金模式
                  </label>
                  <div className="flex gap-2">
                    <button
                      type="button"
                      onClick={() => handleInputChange('is_cross_margin', true)}
                      className={`flex-1 px-3 py-2 rounded text-sm ${
                        formData.is_cross_margin
                          ? 'bg-[#F0B90B] text-black'
                          : 'bg-[#0B0E11] text-[#848E9C] border border-[#2B3139]'
                      }`}
                    >
                      全仓
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        handleInputChange('is_cross_margin', false)
                      }
                      className={`flex-1 px-3 py-2 rounded text-sm ${
                        !formData.is_cross_margin
                          ? 'bg-[#F0B90B] text-black'
                          : 'bg-[#0B0E11] text-[#848E9C] border border-[#2B3139]'
                      }`}
                    >
                      逐仓
                    </button>
                  </div>
                </div>
                <div>
                  <label className="text-sm text-[#EAECEF] block mb-2">
                    {t('aiScanInterval', language)}
                  </label>
                  <input
                    type="number"
                    value={formData.scan_interval_minutes}
                    onChange={(e) => {
                      const parsedValue = Number(e.target.value)
                      const safeValue = Number.isFinite(parsedValue)
                        ? Math.max(3, parsedValue)
                        : 3
                      handleInputChange('scan_interval_minutes', safeValue)
                    }}
                    className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
                    min="3"
                    max="60"
                    step="1"
                  />
                  <p className="text-xs text-gray-500 mt-1">
                    {t('scanIntervalRecommend', language)}
                  </p>
                </div>
                <div>
                  <label className="text-sm text-[#EAECEF] block mb-2">
                    {language === 'zh' ? '自适应扫描间隔' : 'Adaptive Scan Interval'}
                  </label>
                  <div className="flex gap-2">
                    <button
                      type="button"
                      onClick={() => handleInputChange('adaptive_interval', false)}
                      className={`flex-1 px-3 py-2 rounded text-sm ${
                        !formData.adaptive_interval
                          ? 'bg-[#F0B90B] text-black'
                          : 'bg-[#0B0E11] text-[#848E9C] border border-[#2B3139]'
                      }`}
                    >
                      {language === 'zh' ? '关闭（严格按间隔）' : 'Off (strict)'}
                    </button>
                    <button
                      type="button"
                      onClick={() => handleInputChange('adaptive_interval', true)}
                      className={`flex-1 px-3 py-2 rounded text-sm ${
                        formData.adaptive_interval
                          ? 'bg-[#F0B90B] text-black'
                          : 'bg-[#0B0E11] text-[#848E9C] border border-[#2B3139]'
                      }`}
                    >
                      {language === 'zh' ? '开启' : 'On'}
                    </button>
                  </div>
                  <p className="text-xs text-gray-500 mt-1">
                    {language === 'zh'
                      ? '开启后系统会根据市场波动自动加速或放慢扫描（可能远快于上方间隔，消耗更多AI调用）。默认关闭。'
                      : 'When enabled, the system dynamically speeds up or slows down scanning based on market volatility (may scan much faster than the interval above and consume more AI calls). Default off.'}
                  </p>
                </div>
                <div
                  className={`rounded-lg p-2 -m-2 border ${
                    formData.giveback_mode !== 'off'
                      ? 'border-[#F0B90B]/60 bg-[#F0B90B]/5'
                      : 'border-transparent'
                  }`}
                >
                  <label
                    className="text-sm block mb-2 flex items-center gap-1"
                    style={{ color: formData.giveback_mode !== 'off' ? '#F0B90B' : '#EAECEF' }}
                  >
                    🛡️ {language === 'zh' ? '浮盈回撤保护' : 'Profit Giveback Protection'}
                  </label>
                  <div className="flex gap-2">
                    {([
                      { value: 'off', zh: '关闭', en: 'Off' },
                      { value: 'soft', zh: '软规则（AI决定）', en: 'Soft (AI decides)' },
                      { value: 'hard', zh: '硬规则（固定值）', en: 'Hard (fixed)' },
                    ]).map(opt => (
                      <button
                        key={opt.value}
                        type="button"
                        onClick={() => handleInputChange('giveback_mode', opt.value)}
                        className={`flex-1 px-3 py-2 rounded text-sm ${
                          formData.giveback_mode === opt.value
                            ? 'bg-[#F0B90B] text-black'
                            : 'bg-[#0B0E11] text-[#848E9C] border border-[#2B3139]'
                        }`}
                      >
                        {language === 'zh' ? opt.zh : opt.en}
                      </button>
                    ))}
                  </div>
                  <p className="text-xs text-gray-500 mt-1">
                    {language === 'zh'
                      ? '追踪持仓浮盈峰值，浮盈从峰值回落超过阈值时程序自动市价平仓（每30秒检查一次）。'
                      : 'Tracks peak unrealized profit; the position is auto-closed at market when profit falls back beyond the threshold (checked every 30s).'}
                  </p>
                  {formData.giveback_mode === 'hard' && (
                    <div className="mt-3">
                      <label className="text-sm text-[#EAECEF] block mb-2">
                        {language === 'zh' ? '回撤阈值（%）' : 'Giveback threshold (%)'}
                      </label>
                      <input
                        type="number"
                        value={formData.giveback_hard_pct}
                        onChange={(e) => {
                          const parsed = Number(e.target.value)
                          const safe = Number.isFinite(parsed) ? Math.min(90, Math.max(1, parsed)) : 30
                          handleInputChange('giveback_hard_pct', safe)
                        }}
                        className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
                        min="1"
                        max="90"
                        step="1"
                      />
                      <p className="text-xs text-gray-500 mt-1">
                        {language === 'zh'
                          ? '对所有新开仓强制生效，AI 无法修改。软规则模式下由 AI 每次开仓自行决定。关闭时提示词中完全不出现该规则。'
                          : 'Enforced on every new position; AI cannot override. In Soft mode AI decides per trade. When Off, this rule is hidden from the AI prompt entirely.'}
                      </p>
                    </div>
                  )}
                </div>
                <div>
                  <label className="text-sm text-[#EAECEF] block mb-2">
                    {language === 'zh' ? '交易模式' : 'Trading Mode'}
                  </label>
                  <select
                    value={formData.trading_mode}
                    onChange={(e) =>
                      handleInputChange('trading_mode', e.target.value)
                    }
                    className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none"
                  >
                    <option value="">
                      {language === 'zh' ? '默认 (Balanced)' : 'Default (Balanced)'}
                    </option>
                    <option value="balanced">Balanced</option>
                    <option value="aggressive">
                      {language === 'zh' ? '激进 (Aggressive)' : 'Aggressive'}
                    </option>
                    <option value="conservative">
                      {language === 'zh' ? '保守 (Conservative)' : 'Conservative'}
                    </option>
                  </select>
                  <p className="text-xs text-gray-500 mt-1">
                    {language === 'zh'
                      ? '选择AI的交易风格。默认为平衡模式。'
                      : 'Select AI trading style. Default is balanced mode.'}
                  </p>
                </div>
              </div>

              {/* Competition visibility */}
              <div>
                <label className="text-sm text-[#EAECEF] block mb-2">
                  竞技场显示
                </label>
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={() => handleInputChange('show_in_competition', true)}
                    className={`flex-1 px-3 py-2 rounded text-sm ${
                      formData.show_in_competition
                        ? 'bg-[#F0B90B] text-black'
                        : 'bg-[#0B0E11] text-[#848E9C] border border-[#2B3139]'
                    }`}
                  >
                    显示
                  </button>
                  <button
                    type="button"
                    onClick={() => handleInputChange('show_in_competition', false)}
                    className={`flex-1 px-3 py-2 rounded text-sm ${
                      !formData.show_in_competition
                        ? 'bg-[#F0B90B] text-black'
                        : 'bg-[#0B0E11] text-[#848E9C] border border-[#2B3139]'
                    }`}
                  >
                    隐藏
                  </button>
                </div>
                <p className="text-xs text-[#848E9C] mt-1">
                  隐藏后将不在竞技场页面显示此交易员
                </p>
              </div>

              {/* Feedback Analysis */}
              <div>
                <label className="text-sm text-[#EAECEF] block mb-2">
                  {language === 'zh' ? '分析功能' : 'Analysis Features'}
                </label>
                <div className="space-y-2">
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={formData.enable_feedback}
                      onChange={(e) => handleInputChange('enable_feedback', e.target.checked)}
                      className="accent-[#F0B90B]"
                    />
                    <span className="text-sm text-[#EAECEF]">
                      {language === 'zh' ? '启用反馈分析' : 'Enable Feedback Analysis'}
                    </span>
                  </label>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={formData.enable_llm_feedback}
                      onChange={(e) => handleInputChange('enable_llm_feedback', e.target.checked)}
                      disabled={!formData.enable_feedback}
                      className="accent-[#F0B90B] disabled:opacity-50"
                    />
                    <span className="text-sm text-[#EAECEF]">
                      {language === 'zh' ? '启用LLM反馈分析' : 'Enable LLM Feedback'}
                    </span>
                  </label>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={formData.enable_prompt_evolution}
                      onChange={(e) => handleInputChange('enable_prompt_evolution', e.target.checked)}
                      className="accent-[#F0B90B]"
                    />
                    <span className="text-sm text-[#EAECEF]">
                      {language === 'zh' ? '启用提示词进化' : 'Enable Prompt Evolution'}
                    </span>
                  </label>
                </div>
                <p className="text-xs text-[#848E9C] mt-1">
                  {language === 'zh'
                    ? '反馈分析: 学习过往交易决策; 提示词进化: 自动优化交易提示词'
                    : 'Feedback: Learn from past trading decisions; Evolution: Auto-optimize prompts'}
                </p>
              </div>

              {/* Initial Balance (Edit mode only) */}
              {isEditMode && (
                <div>
                  <div className="flex items-center justify-between mb-2">
                    <label className="text-sm text-[#EAECEF]">
                      {formData.paper_trading ? '模拟初始资金 (USDT)' : '初始余额 ($)'}
                    </label>
                    {!formData.paper_trading && (
                      <button
                        type="button"
                        onClick={handleFetchCurrentBalance}
                        disabled={isFetchingBalance}
                        className="px-3 py-1 text-xs bg-[#F0B90B] text-black rounded hover:bg-[#E1A706] transition-colors disabled:bg-[#848E9C] disabled:cursor-not-allowed"
                      >
                        {isFetchingBalance ? '获取中...' : '获取当前余额'}
                      </button>
                    )}
                  </div>
                  <input
                    type="number"
                    value={formData.initial_balance || 0}
                    onChange={(e) =>
                      handleInputChange(
                        'initial_balance',
                        Number(e.target.value)
                      )
                    }
                    disabled={!!formData.paper_trading}
                    className="w-full px-3 py-2 bg-[#0B0E11] border border-[#2B3139] rounded text-[#EAECEF] focus:border-[#F0B90B] focus:outline-none disabled:opacity-60"
                    min="100"
                    step="0.01"
                  />
                  <p className="text-xs text-[#848E9C] mt-1">
                    {formData.paper_trading
                      ? '模拟盘初始资金在创建时设定，编辑仅用于盈亏统计基准；如需重来请使用「重置模拟账户」'
                      : '用于手动更新初始余额基准（例如充值/提现后）'}
                  </p>
                  {balanceFetchError && (
                    <p className="text-xs text-red-500 mt-1">
                      {balanceFetchError}
                    </p>
                  )}
                  {formData.paper_trading && (
                    <button
                      type="button"
                      onClick={async () => {
                        if (!traderData?.trader_id) return
                        if (!window.confirm('确定重置模拟账户？将清空全部模拟持仓/订单记录并恢复初始资金')) return
                        try {
                          const { api } = await import('../lib/api')
                          await toast.promise(api.resetPaperAccount(traderData.trader_id), {
                            loading: '正在重置…',
                            success: '模拟账户已重置',
                            error: '重置失败',
                          })
                        } catch (e) {
                          console.error('重置模拟账户失败:', e)
                        }
                      }}
                      className="mt-2 w-full px-3 py-2 text-sm bg-transparent border border-red-500/60 text-red-400 rounded hover:bg-red-500/10 transition-colors"
                    >
                      🎮 重置模拟账户（清空记录并恢复初始资金）
                    </button>
                  )}
                </div>
              )}

              {/* Create mode info */}
              {!isEditMode && !formData.paper_trading && (
                <div className="p-3 bg-[#1E2329] border border-[#2B3139] rounded flex items-center gap-2">
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    className="w-4 h-4 text-[#F0B90B]"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <circle cx="12" cy="12" r="10" />
                    <line x1="12" x2="12" y1="8" y2="12" />
                    <line x1="12" x2="12.01" y1="16" y2="16" />
                  </svg>
                  <span className="text-sm text-[#848E9C]">
                    系统将自动获取您的账户净值作为初始余额
                  </span>
                </div>
              )}
            </div>
          </div>

        </div>

        {/* Footer */}
        <div className="flex justify-end gap-3 p-6 border-t border-[#2B3139] bg-gradient-to-r from-[#1E2329] to-[#252B35] sticky bottom-0 z-10 rounded-b-xl">
          <button
            onClick={onClose}
            className="px-6 py-3 bg-[#2B3139] text-[#EAECEF] rounded-lg hover:bg-[#404750] transition-all duration-200 border border-[#404750]"
          >
            取消
          </button>
          {onSave && (
            <button
              onClick={handleSave}
              disabled={
                isSaving ||
                !formData.trader_name ||
                !formData.ai_model ||
                (!formData.exchange_id && !formData.paper_trading) ||
                (formData.paper_trading && !isEditMode && !(formData.initial_balance && formData.initial_balance > 0))
              }
              className="px-8 py-3 bg-gradient-to-r from-[#F0B90B] to-[#E1A706] text-black rounded-lg hover:from-[#E1A706] hover:to-[#D4951E] transition-all duration-200 disabled:bg-[#848E9C] disabled:cursor-not-allowed font-medium shadow-lg"
            >
              {isSaving ? '保存中...' : isEditMode ? '保存修改' : '创建交易员'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
