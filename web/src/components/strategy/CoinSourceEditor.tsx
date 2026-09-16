import { useState } from 'react'
import { Plus, X, Database, TrendingUp, List, Link, AlertCircle, Zap } from 'lucide-react'
import type { CoinSourceConfig } from '../../types'
import { getDefaultCoinPoolAPIURL, getDefaultOITopAPIURL } from '../../config/apiConstants'

// Default API URLs for data sources (using new https://nofxos.ai base URL)
const DEFAULT_COIN_POOL_API_URL = getDefaultCoinPoolAPIURL()
const DEFAULT_OI_TOP_API_URL = getDefaultOITopAPIURL(20, '1h')

// 热门币快捷添加列表
const POPULAR_COINS = ['BTC', 'ETH', 'SOL', 'BNB', 'XRP', 'DOGE', 'ADA', 'AVAX', 'DOT', 'LINK']

interface CoinSourceEditorProps {
  config: CoinSourceConfig
  onChange: (config: CoinSourceConfig) => void
  disabled?: boolean
  language: string
}

export function CoinSourceEditor({
  config,
  onChange,
  disabled,
  language,
}: CoinSourceEditorProps) {
  const [newCoin, setNewCoin] = useState('')
  const [dupWarning, setDupWarning] = useState('')

  const t = (key: string) => {
    const translations: Record<string, Record<string, string>> = {
      sourceType: { zh: '数据来源类型', en: 'Source Type' },
      static: { zh: '静态列表', en: 'Static List' },
      coinpool: { zh: 'AI500 数据源', en: 'AI500 Data Provider' },
      oi_top: { zh: 'OI Top 持仓增长', en: 'OI Top' },
      mixed: { zh: '混合模式', en: 'Mixed Mode' },
      staticCoins: { zh: '自定义币种', en: 'Custom Coins' },
      addCoin: { zh: '添加币种', en: 'Add Coin' },
      hotCoins: { zh: '快捷添加', en: 'Quick Add' },
      coinsAdded: { zh: '个币种', en: 'coins' },
      coinExists: { zh: '该币种已在列表中', en: 'Coin already in list' },
      someSkipped: { zh: '部分币种已存在，已自动跳过', en: 'Some coins already exist and were skipped' },
      batchHint: {
        zh: '支持批量添加：用逗号或空格分隔，如 BTC, ETH, SOL',
        en: 'Batch add supported: separate with commas or spaces, e.g. BTC, ETH, SOL',
      },
      useCoinPool: { zh: '启用 AI500 数据源', en: 'Enable AI500 Data Provider' },
      coinPoolLimit: { zh: '数据源数量上限', en: 'Data Provider Limit' },
      coinPoolApiUrl: { zh: 'AI500 API URL', en: 'AI500 API URL' },
      coinPoolApiUrlPlaceholder: { zh: '输入 AI500 数据源 API 地址...', en: 'Enter AI500 data provider API URL...' },
      useOITop: { zh: '启用 OI Top 数据', en: 'Enable OI Top' },
      oiTopLimit: { zh: 'OI Top 数量上限', en: 'OI Top Limit' },
      oiTopApiUrl: { zh: 'OI Top API URL', en: 'OI Top API URL' },
      oiTopApiUrlPlaceholder: { zh: '输入 OI Top 持仓数据 API 地址...', en: 'Enter OI Top API URL...' },
      staticDesc: { zh: '手动指定交易币种列表', en: 'Manually specify trading coins' },
      coinpoolDesc: {
        zh: '使用 AI500 智能筛选的热门币种',
        en: 'Use AI500 smart-filtered popular coins',
      },
      oiTopDesc: {
        zh: '使用持仓量增长最快的币种',
        en: 'Use coins with fastest OI growth',
      },
      mixedDesc: {
        zh: '组合多种数据源，AI500 + OI Top + 自定义',
        en: 'Combine multiple sources: AI500 + OI Top + Custom',
      },
      apiUrlRequired: { zh: '需要填写 API URL 才能获取数据', en: 'API URL required to fetch data' },
      dataSourceConfig: { zh: '数据源配置', en: 'Data Source Configuration' },
      fillDefault: { zh: '填入默认', en: 'Fill Default' },
    }
    return translations[key]?.[language] || key
  }

  const sourceTypes = [
    { value: 'static', icon: List, color: '#848E9C' },
    { value: 'coinpool', icon: Database, color: '#F0B90B' },
    { value: 'oi_top', icon: TrendingUp, color: '#0ECB81' },
    { value: 'mixed', icon: Database, color: '#60a5fa' },
  ] as const

  // 格式化输入币种：大写 + 自动补 USDT 后缀
  const formatCoin = (raw: string): string => {
    const symbol = raw.toUpperCase().trim()
    return symbol.endsWith('USDT') ? symbol : `${symbol}USDT`
  }

  // 批量添加：支持逗号/空格分隔，自动去重
  const handleAddCoins = () => {
    const parts = newCoin
      .split(/[,，\s]+/)
      .map((s) => s.trim())
      .filter(Boolean)
    if (parts.length === 0) return

    const currentCoins = config.static_coins || []
    const toAdd: string[] = []
    let skipped = 0
    parts.forEach((p) => {
      const formatted = formatCoin(p)
      if (currentCoins.includes(formatted) || toAdd.includes(formatted)) {
        skipped++
      } else {
        toAdd.push(formatted)
      }
    })

    if (toAdd.length > 0) {
      onChange({ ...config, static_coins: [...currentCoins, ...toAdd] })
    }
    setDupWarning(skipped > 0 ? (toAdd.length > 0 ? 'partial' : 'dup') : '')
    setNewCoin('')
  }

  const handleRemoveCoin = (coin: string) => {
    onChange({
      ...config,
      static_coins: (config.static_coins || []).filter((c) => c !== coin),
    })
    setDupWarning('')
  }

  // 热门币快捷开关：未添加则添加，已添加则移除
  const handleTogglePopular = (coin: string) => {
    const formatted = formatCoin(coin)
    const currentCoins = config.static_coins || []
    if (currentCoins.includes(formatted)) {
      handleRemoveCoin(formatted)
    } else {
      onChange({ ...config, static_coins: [...currentCoins, formatted] })
      setDupWarning('')
    }
  }

  // 输入框实时重复检测（单个币种输入时）
  const inputDuplicate =
    newCoin.trim() && (config.static_coins || []).includes(formatCoin(newCoin))

  return (
    <div className="space-y-6">
      {/* Source Type Selector */}
      <div>
        <label className="block text-sm font-medium mb-3" style={{ color: '#EAECEF' }}>
          {t('sourceType')}
        </label>
        <div className="grid grid-cols-4 gap-3">
          {sourceTypes.map(({ value, icon: Icon, color }) => (
            <button
              key={value}
              onClick={() =>
                !disabled &&
                onChange({ ...config, source_type: value as CoinSourceConfig['source_type'] })
              }
              disabled={disabled}
              className={`p-4 rounded-lg border transition-all ${
                config.source_type === value
                  ? 'ring-2 ring-yellow-500'
                  : 'hover:bg-white/5'
              }`}
              style={{
                background:
                  config.source_type === value
                    ? 'rgba(240, 185, 11, 0.1)'
                    : '#0B0E11',
                borderColor: '#2B3139',
              }}
            >
              <Icon className="w-6 h-6 mx-auto mb-2" style={{ color }} />
              <div className="text-sm font-medium" style={{ color: '#EAECEF' }}>
                {t(value)}
              </div>
              <div className="text-xs mt-1" style={{ color: '#848E9C' }}>
                {t(`${value}Desc`)}
              </div>
            </button>
          ))}
        </div>
      </div>

      {/* Static Coins */}
      {(config.source_type === 'static' || config.source_type === 'mixed') && (
        <div>
          {/* 标签行：标题 + 计数徽章 */}
          <div className="flex items-center gap-2 mb-3">
            <label className="text-sm font-medium" style={{ color: '#EAECEF' }}>
              {t('staticCoins')}
            </label>
            {(config.static_coins || []).length > 0 && (
              <span
                className="text-xs px-2 py-0.5 rounded-full font-medium"
                style={{ background: 'rgba(240, 185, 11, 0.12)', color: '#F0B90B' }}
              >
                {(config.static_coins || []).length} {t('coinsAdded')}
              </span>
            )}
          </div>

          {/* 已添加币种胶囊列表 */}
          <div className="flex flex-wrap gap-2 mb-3">
            {(config.static_coins || []).map((coin) => (
              <span
                key={coin}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-full text-sm transition-all"
                style={{
                  background: 'rgba(240, 185, 11, 0.08)',
                  border: '1px solid rgba(240, 185, 11, 0.25)',
                  color: '#EAECEF',
                }}
              >
                {coin}
                {!disabled && (
                  <button
                    onClick={() => handleRemoveCoin(coin)}
                    className="hover:text-red-400 transition-colors"
                    style={{ color: '#848E9C' }}
                  >
                    <X className="w-3 h-3" />
                  </button>
                )}
              </span>
            ))}
            {(config.static_coins || []).length === 0 && (
              <span className="text-xs px-3 py-1.5 rounded-full" style={{ color: '#5E6673', border: '1px dashed #2B3139' }}>
                {language === 'zh' ? '尚未添加币种' : 'No coins added yet'}
              </span>
            )}
          </div>

          {!disabled && (
            <>
              {/* 热门币快捷添加 */}
              <div className="flex flex-wrap items-center gap-1.5 mb-3">
                <span className="flex items-center gap-1 text-xs mr-1" style={{ color: '#848E9C' }}>
                  <Zap className="w-3 h-3" style={{ color: '#F0B90B' }} />
                  {t('hotCoins')}
                </span>
                {POPULAR_COINS.map((coin) => {
                  const added = (config.static_coins || []).includes(formatCoin(coin))
                  return (
                    <button
                      key={coin}
                      type="button"
                      onClick={() => handleTogglePopular(coin)}
                      className="px-2.5 py-1 rounded-full text-xs font-medium transition-all"
                      style={
                        added
                          ? {
                              background: 'rgba(240, 185, 11, 0.15)',
                              border: '1px solid rgba(240, 185, 11, 0.5)',
                              color: '#F0B90B',
                            }
                          : {
                              background: '#0B0E11',
                              border: '1px solid #2B3139',
                              color: '#848E9C',
                            }
                      }
                    >
                      {coin}
                    </button>
                  )
                })}
              </div>

              {/* 手动输入（支持批量） */}
              <div className="flex gap-2">
                <input
                  type="text"
                  value={newCoin}
                  onChange={(e) => {
                    setNewCoin(e.target.value)
                    setDupWarning('')
                  }}
                  onKeyDown={(e) => e.key === 'Enter' && handleAddCoins()}
                  placeholder="BTC, ETH, SOL..."
                  className="flex-1 px-4 py-2 rounded-lg"
                  style={{
                    background: '#0B0E11',
                    border: inputDuplicate ? '1px solid rgba(240, 185, 11, 0.5)' : '1px solid #2B3139',
                    color: '#EAECEF',
                  }}
                />
                <button
                  onClick={handleAddCoins}
                  className="px-4 py-2 rounded-lg flex items-center gap-2 transition-colors"
                  style={{ background: '#F0B90B', color: '#0B0E11' }}
                >
                  <Plus className="w-4 h-4" />
                  {t('addCoin')}
                </button>
              </div>
              <div className="text-xs mt-2" style={{ color: '#5E6673' }}>
                {t('batchHint')}
              </div>
              {/* 重复提示 */}
              {(inputDuplicate || dupWarning) && (
                <div className="flex items-center gap-1.5 mt-2">
                  <AlertCircle className="w-3.5 h-3.5" style={{ color: '#F0B90B' }} />
                  <span className="text-xs" style={{ color: '#F0B90B' }}>
                    {inputDuplicate ? t('coinExists') : t('someSkipped')}
                  </span>
                </div>
              )}
            </>
          )}
        </div>
      )}

      {/* Coin Pool Options */}
      {(config.source_type === 'coinpool' || config.source_type === 'mixed') && (
        <div className="space-y-4">
          <div className="flex items-center gap-2 mb-2">
            <Link className="w-4 h-4" style={{ color: '#F0B90B' }} />
            <span className="text-sm font-medium" style={{ color: '#EAECEF' }}>
              {t('dataSourceConfig')} - AI500
            </span>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="flex items-center gap-3 mb-3 cursor-pointer">
                <input
                  type="checkbox"
                  checked={config.use_coin_pool}
                  onChange={(e) =>
                    !disabled && onChange({ ...config, use_coin_pool: e.target.checked })
                  }
                  disabled={disabled}
                  className="w-5 h-5 rounded accent-yellow-500"
                />
                <span style={{ color: '#EAECEF' }}>{t('useCoinPool')}</span>
              </label>
              {config.use_coin_pool && (
                <div className="flex items-center gap-3">
                  <span className="text-sm" style={{ color: '#848E9C' }}>
                    {t('coinPoolLimit')}:
                  </span>
                  <input
                    type="number"
                    value={config.coin_pool_limit || 10}
                    onChange={(e) =>
                      !disabled &&
                      onChange({ ...config, coin_pool_limit: parseInt(e.target.value) || 10 })
                    }
                    disabled={disabled}
                    min={1}
                    max={100}
                    className="w-20 px-3 py-1.5 rounded"
                    style={{
                      background: '#0B0E11',
                      border: '1px solid #2B3139',
                      color: '#EAECEF',
                    }}
                  />
                </div>
              )}
            </div>
          </div>

          {config.use_coin_pool && (
            <div>
              <div className="flex items-center justify-between mb-2">
                <label className="text-sm" style={{ color: '#848E9C' }}>
                  {t('coinPoolApiUrl')}
                </label>
                {!disabled && !config.coin_pool_api_url && (
                  <button
                    type="button"
                    onClick={() => onChange({ ...config, coin_pool_api_url: DEFAULT_COIN_POOL_API_URL })}
                    className="text-xs px-2 py-1 rounded"
                    style={{ background: '#F0B90B20', color: '#F0B90B' }}
                  >
                    {t('fillDefault')}
                  </button>
                )}
              </div>
              <input
                type="url"
                value={config.coin_pool_api_url || ''}
                onChange={(e) =>
                  !disabled && onChange({ ...config, coin_pool_api_url: e.target.value })
                }
                disabled={disabled}
                placeholder={t('coinPoolApiUrlPlaceholder')}
                className="w-full px-4 py-2.5 rounded-lg font-mono text-sm"
                style={{
                  background: '#0B0E11',
                  border: '1px solid #2B3139',
                  color: '#EAECEF',
                }}
              />
              {!config.coin_pool_api_url && (
                <div className="flex items-center gap-2 mt-2">
                  <AlertCircle className="w-4 h-4" style={{ color: '#F0B90B' }} />
                  <span className="text-xs" style={{ color: '#F0B90B' }}>
                    {t('apiUrlRequired')}
                  </span>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* OI Top Options */}
      {(config.source_type === 'oi_top' || config.source_type === 'mixed') && (
        <div className="space-y-4">
          <div className="flex items-center gap-2 mb-2">
            <Link className="w-4 h-4" style={{ color: '#0ECB81' }} />
            <span className="text-sm font-medium" style={{ color: '#EAECEF' }}>
              {t('dataSourceConfig')} - OI Top
            </span>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="flex items-center gap-3 mb-3 cursor-pointer">
                <input
                  type="checkbox"
                  checked={config.use_oi_top}
                  onChange={(e) =>
                    !disabled && onChange({ ...config, use_oi_top: e.target.checked })
                  }
                  disabled={disabled}
                  className="w-5 h-5 rounded accent-yellow-500"
                />
                <span style={{ color: '#EAECEF' }}>{t('useOITop')}</span>
              </label>
              {config.use_oi_top && (
                <div className="flex items-center gap-3">
                  <span className="text-sm" style={{ color: '#848E9C' }}>
                    {t('oiTopLimit')}:
                  </span>
                  <input
                    type="number"
                    value={config.oi_top_limit || 20}
                    onChange={(e) =>
                      !disabled &&
                      onChange({ ...config, oi_top_limit: parseInt(e.target.value) || 20 })
                    }
                    disabled={disabled}
                    min={1}
                    max={50}
                    className="w-20 px-3 py-1.5 rounded"
                    style={{
                      background: '#0B0E11',
                      border: '1px solid #2B3139',
                      color: '#EAECEF',
                    }}
                  />
                </div>
              )}
            </div>
          </div>

          {config.use_oi_top && (
            <div>
              <div className="flex items-center justify-between mb-2">
                <label className="text-sm" style={{ color: '#848E9C' }}>
                  {t('oiTopApiUrl')}
                </label>
                {!disabled && !config.oi_top_api_url && (
                  <button
                    type="button"
                    onClick={() => onChange({ ...config, oi_top_api_url: DEFAULT_OI_TOP_API_URL })}
                    className="text-xs px-2 py-1 rounded"
                    style={{ background: '#0ECB8120', color: '#0ECB81' }}
                  >
                    {t('fillDefault')}
                  </button>
                )}
              </div>
              <input
                type="url"
                value={config.oi_top_api_url || ''}
                onChange={(e) =>
                  !disabled && onChange({ ...config, oi_top_api_url: e.target.value })
                }
                disabled={disabled}
                placeholder={t('oiTopApiUrlPlaceholder')}
                className="w-full px-4 py-2.5 rounded-lg font-mono text-sm"
                style={{
                  background: '#0B0E11',
                  border: '1px solid #2B3139',
                  color: '#EAECEF',
                }}
              />
              {!config.oi_top_api_url && (
                <div className="flex items-center gap-2 mt-2">
                  <AlertCircle className="w-4 h-4" style={{ color: '#F0B90B' }} />
                  <span className="text-xs" style={{ color: '#F0B90B' }}>
                    {t('apiUrlRequired')}
                  </span>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
