import { LaptopIcon, MoonIcon, SunIcon } from 'lucide-react'

import type { Theme } from '@/lib/theme'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'

const OPTIONS: Array<{ value: Theme; label: string; blurb: string; icon: typeof SunIcon }> = [
  { value: 'light', label: 'Claro', blurb: 'Papel y tinta, como hoy', icon: SunIcon },
  { value: 'dark', label: 'Oscuro', blurb: 'La misma libreta, leída de noche', icon: MoonIcon },
  { value: 'system', label: 'Sistema', blurb: 'Sigue lo que diga tu dispositivo', icon: LaptopIcon },
]

/**
 * Ajustes: the one preference corebank has that isn't about money.
 *
 * A single section today — Apariencia — rather than a page that anticipates
 * settings nobody asked for yet. This is where the next one goes, not a
 * skeleton built for them in advance.
 */
export function AjustesPage() {
  const { theme, setTheme } = useTheme()

  return (
    <div className="mx-auto max-w-2xl">
      <header>
        <p className="type-eyebrow">Ajustes</p>
        <h1 className="type-display mt-1.5 text-[1.75rem]">Apariencia</h1>
        <p className="mt-1.5 text-ink-soft">
          Elige cómo se ve corebank en este dispositivo.
        </p>
      </header>

      <div className="mt-6 card p-5">
        <fieldset className="grid gap-2 sm:grid-cols-3">
          <legend className="sr-only">Apariencia</legend>
          {OPTIONS.map((option) => (
            <label
              key={option.value}
              className={cn(
                'flex cursor-pointer items-start gap-3 rounded-[5px] border px-3.5 py-3 transition-colors',
                theme === option.value
                  ? 'border-copper bg-copper/8'
                  : 'border-rule bg-paper-raised hover:border-ink-faint',
              )}
            >
              <input
                type="radio"
                name="theme"
                value={option.value}
                checked={theme === option.value}
                onChange={() => setTheme(option.value)}
                className="sr-only"
              />
              <option.icon
                className="mt-0.5 size-4 shrink-0 text-ink-soft"
                aria-hidden="true"
              />
              <span className="min-w-0 flex-1">
                <span className="block text-[0.9375rem] font-medium">{option.label}</span>
                <span className="mt-0.5 block text-[0.75rem] text-ink-faint">
                  {option.blurb}
                </span>
              </span>
              <span
                aria-hidden="true"
                className={cn(
                  'mt-1 size-3.5 shrink-0 rounded-full border',
                  theme === option.value ? 'border-[5px] border-copper' : 'border-rule',
                )}
              />
            </label>
          ))}
        </fieldset>
      </div>
    </div>
  )
}
