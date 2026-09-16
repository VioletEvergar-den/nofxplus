import { PromptVariantLab } from './PromptVariantLab'

// Re-export types for backward compatibility
export type { PromptVariant } from './PromptVariantLab'
export type { PromptVariantLabResponse as TraderPromptVariantsResponse } from './PromptVariantLab'
export type { PromptVariantPerformanceResponse as TraderPromptVariantResponse } from './PromptVariantLab'

interface LiveTraderPromptLabProps {
  traderId?: string
  showHeader?: boolean
}

export function LiveTraderPromptLab({ traderId, showHeader }: LiveTraderPromptLabProps) {
  return <PromptVariantLab type="trader" resourceId={traderId} showHeader={showHeader} />
}
