import type { Amount } from '@/api/types'
import { money, moneyParts } from '@/lib/format'
import { cn } from '@/lib/utils'

// ---------------------------------------------------------------------------
// Money
// ---------------------------------------------------------------------------

/**
 * Figure renders an amount with the cents set smaller than the dollars.
 *
 * The cents are what you check, not what you read, so they are subordinate — which
 * lets the dollars carry the display size without the whole number shouting.
 */
export function Figure({
  amount,
  size = 'md',
  tone = 'ink',
  className,
}: {
  amount: Amount | number
  size?: 'sm' | 'md' | 'lg' | 'display'
  tone?: 'ink' | 'credit' | 'hold' | 'faint' | 'copper'
  className?: string
}) {
  const { whole, cents } = moneyParts(amount)

  const sizes = {
    sm: 'text-[0.8125rem]',
    md: 'text-base',
    lg: 'text-[1.375rem]',
    display: 'text-[clamp(2.25rem,7vw,3.5rem)]',
  }
  const tones = {
    ink: 'text-ink',
    credit: 'text-credit',
    hold: 'text-hold',
    faint: 'text-ink-faint',
    copper: 'text-copper',
  }

  return (
    <span
      className={cn('type-figure whitespace-nowrap', sizes[size], tones[tone], className)}
      // The full value stays available to a screen reader as one number, rather
      // than being announced as two fragments.
      aria-label={money(amount)}
    >
      <span aria-hidden="true">{whole}</span>
      <span
        aria-hidden="true"
        className={size === 'display' ? 'text-[0.55em]' : 'text-[0.8em]'}
      >
        {cents}
      </span>
    </span>
  )
}
