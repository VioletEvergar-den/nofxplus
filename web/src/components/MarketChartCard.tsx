import { useState, useEffect, useRef } from 'react'
import { AdvancedChart } from './AdvancedChart'
import { useLanguage } from '../contexts/LanguageContext'
import { t } from '../i18n/translations'
import { CandlestickChart, ChevronDown, Search } from 'lucide-react'

interface MarketChartCardProps {
  traderId: string
  selectedSymbol?: string // 从外部选择的币种
  updateKey?: number // 强制更新的 key
  exchangeId?: string // 交易所ID
}

type Interval = '1m' | '5m' | '15m' | '30m' | '1h' | '4h' | '1d'
type MarketType = 'hyperliquid' | 'crypto'

interface SymbolInfo {
  symbol: string
  name: string
  category: string
}

// 市场类型配置
const MARKET_CONFIG = {
  hyperliquid: { exchange: 'hyperliquid', defaultSymbol: 'BTC', icon: '🔷', label: { zh: 'HL', en: 'HL' }, hasDropdown: true },
  crypto: { exchange: 'binance', defaultSymbol: 'BTCUSDT', icon: '₿', label: { zh: '加密', en: 'Crypto' }, hasDropdown: false },
}

const INTERVALS: { value: Interval; label: string }[] = [
  { value: '1m', label: '1m' },
  { value: '5m', label: '5m' },
  { value: '15m', label: '15m' },
  { value: '30m', label: '30m' },
  { value: '1h', label: '1h' },
  { value: '4h', label: '4h' },
  { value: '1d', label: '1d' },
]

// 根据交易所ID推断市场类型
function getMarketTypeFromExchange(exchangeId: string | undefined): MarketType {
  if (!exchangeId) return 'hyperliquid'
  const lower = exchangeId.toLowerCase()
  if (lower.includes('hyperliquid')) return 'hyperliquid'
  // 其他交易所默认使用 crypto 类型
  return 'crypto'
}

// 行情图表卡片（独立组件，从 ChartTabs 拆分而来）
export function MarketChartCard({ traderId, selectedSymbol, updateKey, exchangeId }: MarketChartCardProps) {
  const { language } = useLanguage()
  const [chartSymbol, setChartSymbol] = useState<string>('BTC')
  const [interval, setInterval] = useState<Interval>('5m')
  const [symbolInput, setSymbolInput] = useState('')
  const [marketType, setMarketType] = useState<MarketType>(() => getMarketTypeFromExchange(exchangeId))
  const [availableSymbols, setAvailableSymbols] = useState<SymbolInfo[]>([])
  const [showDropdown, setShowDropdown] = useState(false)
  const [searchFilter, setSearchFilter] = useState('')
  const dropdownRef = useRef<HTMLDivElement>(null)

  // 当交易所ID变化时，自动切换市场类型
  useEffect(() => {
    const newMarketType = getMarketTypeFromExchange(exchangeId)
    setMarketType(newMarketType)
  }, [exchangeId])

  // 根据市场类型确定交易所
  const marketConfig = MARKET_CONFIG[marketType]
  // 优先使用传入的 exchangeId（非 hyperliquid 时）
  const currentExchange = marketType === 'hyperliquid' ? 'hyperliquid' : (exchangeId || marketConfig.exchange)

  // 获取可用币种列表
  useEffect(() => {
    if (marketConfig.hasDropdown) {
      fetch(`/api/symbols?exchange=${marketConfig.exchange}`)
        .then(res => res.json())
        .then(data => {
          if (data.symbols) {
            // 按类别排序: crypto 优先
            const categoryOrder: Record<string, number> = { crypto: 0 }
            const sorted = [...data.symbols].sort((a: SymbolInfo, b: SymbolInfo) => {
              const orderA = categoryOrder[a.category] ?? 5
              const orderB = categoryOrder[b.category] ?? 5
              if (orderA !== orderB) return orderA - orderB
              return a.symbol.localeCompare(b.symbol)
            })
            setAvailableSymbols(sorted)
          }
        })
        .catch(err => console.error('Failed to fetch symbols:', err))
    }
  }, [marketType, marketConfig.exchange, marketConfig.hasDropdown])

  // 点击外部关闭下拉
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setShowDropdown(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  // 切换市场类型时更新默认符号
  const handleMarketTypeChange = (type: MarketType) => {
    setMarketType(type)
    setChartSymbol(MARKET_CONFIG[type].defaultSymbol)
    setShowDropdown(false)
  }

  // 过滤后的币种列表
  const filteredSymbols = availableSymbols.filter(s =>
    s.symbol.toLowerCase().includes(searchFilter.toLowerCase())
  )

  // 当从外部选择币种时，切换行情图表币种
  useEffect(() => {
    if (selectedSymbol) {
      setChartSymbol(selectedSymbol)
    }
  }, [selectedSymbol, updateKey])

  // 处理手动输入符号
  const handleSymbolSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (symbolInput.trim()) {
      let symbol = symbolInput.trim().toUpperCase()
      // 加密货币自动加 USDT 后缀
      if (marketType === 'crypto' && !symbol.endsWith('USDT')) {
        symbol = symbol + 'USDT'
      }
      setChartSymbol(symbol)
      setSymbolInput('')
    }
  }

  return (
    <div
      className="binance-card overflow-hidden"
      style={{ background: '#16181C', borderRadius: '12px', border: '1px solid #2B3139' }}
    >
      {/* 标题栏 + 工具栏 */}
      <div
        className="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5"
        style={{ borderBottom: '1px solid #2B3139', background: '#1E2329' }}
      >
        {/* 左侧：标题 + 市场切换 */}
        <div className="flex items-center gap-2">
          <div
            className="w-8 h-8 rounded-xl flex items-center justify-center shrink-0"
            style={{
              background: 'linear-gradient(135deg, #F0B90B 0%, #FCD535 100%)',
              boxShadow: '0 4px 14px rgba(240, 185, 11, 0.35)',
            }}
          >
            <CandlestickChart className="w-4 h-4" style={{ color: '#16181C' }} />
          </div>
          <span className="text-base font-bold whitespace-nowrap" style={{ color: '#EAECEF' }}>
            {t('marketChart', language)}
          </span>

          {/* 市场类型切换 */}
          <div className="w-px h-4 bg-[#2B3139] mx-1" />
          <div className="flex items-center gap-0.5">
            {(Object.keys(MARKET_CONFIG) as MarketType[]).map((type) => {
              const config = MARKET_CONFIG[type]
              const isActive = marketType === type
              return (
                <button
                  key={type}
                  onClick={() => handleMarketTypeChange(type)}
                  className={`px-2 py-1 text-[11px] font-medium rounded transition-all ${
                    isActive
                      ? 'bg-[rgba(240,185,11,0.15)] text-[#F0B90B]'
                      : 'text-[#848E9C] hover:text-[#EAECEF]'
                  }`}
                >
                  {config.icon} {language === 'zh' ? config.label.zh : config.label.en}
                </button>
              )
            })}
          </div>
        </div>

        {/* 右侧：币种 + 周期 */}
        <div className="flex items-center gap-2">
          {/* 币种下拉 */}
          {marketConfig.hasDropdown ? (
            <div className="relative" ref={dropdownRef}>
              <button
                onClick={() => setShowDropdown(!showDropdown)}
                className="flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-[12px] font-bold text-[#EAECEF] transition-all"
                style={{ background: '#16181C', border: '1px solid #2B3139' }}
              >
                <span>{chartSymbol}</span>
                <ChevronDown className={`w-3 h-3 text-[#848E9C] transition-transform ${showDropdown ? 'rotate-180' : ''}`} />
              </button>
              {showDropdown && (
                <div className="absolute top-full right-0 mt-1 w-56 bg-[#1E2329] border border-[#2B3139] rounded-lg shadow-2xl z-50 max-h-72 overflow-hidden">
                  <div className="p-2 border-b border-[#2B3139]">
                    <div className="flex items-center gap-2 px-2 py-1 rounded border border-[#2B3139]" style={{ background: '#16181C' }}>
                      <Search className="w-3 h-3 text-[#848E9C]" />
                      <input
                        type="text"
                        value={searchFilter}
                        onChange={(e) => setSearchFilter(e.target.value)}
                        placeholder={language === 'zh' ? '搜索币种...' : 'Search...'}
                        className="flex-1 bg-transparent text-[12px] text-[#EAECEF] placeholder-[#5E6673] focus:outline-none"
                        autoFocus
                      />
                    </div>
                  </div>
                  <div className="overflow-y-auto max-h-52">
                    {['crypto'].map(category => {
                      const categorySymbols = filteredSymbols.filter(s => s.category === category)
                      if (categorySymbols.length === 0) return null
                      const labels: Record<string, string> = { crypto: 'Crypto' }
                      return (
                        <div key={category}>
                          <div className="px-3 py-1 text-[10px] font-medium text-[#848E9C] uppercase tracking-wider">{labels[category]}</div>
                          {categorySymbols.map(s => (
                            <button
                              key={s.symbol}
                              onClick={() => { setChartSymbol(s.symbol); setShowDropdown(false); setSearchFilter('') }}
                              className={`w-full px-3 py-1.5 text-left text-[12px] hover:bg-[rgba(240,185,11,0.1)] transition-all ${
                                chartSymbol === s.symbol ? 'text-[#F0B90B]' : 'text-[#EAECEF]'
                              }`}
                            >
                              {s.symbol}
                            </button>
                          ))}
                        </div>
                      )
                    })}
                    {filteredSymbols.length === 0 && (
                      <div className="px-3 py-3 text-[11px] text-[#848E9C] text-center">
                        {language === 'zh' ? '无匹配币种' : 'No symbols found'}
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          ) : (
            <span className="px-2.5 py-1.5 rounded-lg text-[12px] font-bold text-[#EAECEF]" style={{ background: '#16181C', border: '1px solid #2B3139' }}>
              {chartSymbol}
            </span>
          )}

          {/* 周期选择 */}
          <div className="flex items-center rounded-lg overflow-hidden" style={{ background: '#16181C', border: '1px solid #2B3139' }}>
            {INTERVALS.map((int) => (
              <button
                key={int.value}
                onClick={() => setInterval(int.value)}
                className={`px-2 py-1.5 text-[11px] font-medium transition-all ${
                  interval === int.value
                    ? 'bg-[rgba(240,185,11,0.15)] text-[#F0B90B]'
                    : 'text-[#848E9C] hover:text-[#EAECEF]'
                }`}
              >
                {int.label}
              </button>
            ))}
          </div>

          {/* 快速输入 */}
          <form onSubmit={handleSymbolSubmit} className="flex items-center">
            <input
              type="text"
              value={symbolInput}
              onChange={(e) => setSymbolInput(e.target.value)}
              placeholder={language === 'zh' ? '币种...' : 'Symbol...'}
              className="w-20 px-2 py-1.5 rounded-l-lg text-[11px] text-[#EAECEF] placeholder-[#5E6673] focus:outline-none transition-colors focus:border-[#F0B90B]/50"
              style={{ background: '#16181C', border: '1px solid #2B3139', borderRight: 'none' }}
            />
            <button
              type="submit"
              className="px-2 py-1.5 rounded-r-lg text-[11px] text-[#848E9C] hover:text-[#F0B90B] transition-all"
              style={{ background: '#16181C', border: '1px solid #2B3139', borderLeft: 'none' }}
            >
              GO
            </button>
          </form>
        </div>
      </div>

      {/* 图表内容 */}
      <AdvancedChart
        symbol={chartSymbol}
        interval={interval}
        traderID={traderId}
        height={560}
        exchange={currentExchange}
        onSymbolChange={setChartSymbol}
      />
    </div>
  )
}
