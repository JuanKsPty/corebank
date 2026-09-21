import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'

/**
 * The appearance preference, owned above the layout the same way the
 * assistant's docking preference is (`AssistantProvider.tsx`) — a durable
 * choice about how somebody wants to read their own statement, not view
 * state that resets on navigation.
 *
 * The inline script in `index.html` already applied `.dark` (or not) before
 * React ever mounted, so the first render here never has to guess — this
 * provider's job is to keep that class correct as the choice or the OS
 * preference changes afterward, not to set it for the first time.
 */

export type Theme = 'light' | 'dark' | 'system'

const THEME_CYCLE: Record<Theme, Theme> = {
  light: 'dark',
  dark: 'system',
  system: 'light',
}

/** Claro → Oscuro → Sistema → Claro, for the single button that replaced the
 * old three-option Ajustes page — one click, one step forward. */
export function nextTheme(theme: Theme): Theme {
  return THEME_CYCLE[theme]
}

export const THEME_LABEL: Record<Theme, string> = {
  light: 'Claro',
  dark: 'Oscuro',
  system: 'Sistema',
}

interface ThemeContextValue {
  theme: Theme
  setTheme: (value: Theme) => void
  /** What `theme` actually resolves to right now — `system` is never a
   * paint-time answer, callers that need one (sonner's `theme` prop) want
   * this instead. */
  resolvedTheme: 'light' | 'dark'
}

const THEME_KEY = 'corebank.theme'

/** Reads the stored preference, defaulting to following the OS. */
function readTheme(): Theme {
  try {
    const stored = window.localStorage.getItem(THEME_KEY)
    return stored === 'light' || stored === 'dark' || stored === 'system'
      ? stored
      : 'system'
  } catch {
    // Private browsing, or storage disabled. The preference is not important enough to
    // fail over.
    return 'system'
  }
}

/** Tracks the OS preference live, for "Sistema" — switching your whole
 * machine to dark at dusk should not need a second toggle inside corebank. */
function useSystemPrefersDark(): boolean {
  const [prefersDark, setPrefersDark] = useState(
    () => window.matchMedia('(prefers-color-scheme: dark)').matches,
  )

  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => setPrefersDark(media.matches)
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [])

  return prefersDark
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

export function useTheme(): ThemeContextValue {
  const value = useContext(ThemeContext)
  if (!value) throw new Error('useTheme must be used inside a ThemeProvider')
  return value
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readTheme)
  const systemPrefersDark = useSystemPrefersDark()

  const resolvedTheme: 'light' | 'dark' =
    theme === 'system' ? (systemPrefersDark ? 'dark' : 'light') : theme

  useEffect(() => {
    document.documentElement.classList.toggle('dark', resolvedTheme === 'dark')
  }, [resolvedTheme])

  const setTheme = useCallback((value: Theme) => {
    setThemeState(value)
    try {
      window.localStorage.setItem(THEME_KEY, value)
    } catch {
      // Nothing to do: the choice still applies to this visit, it just will
      // not be remembered for the next one.
    }
  }, [])

  const value = useMemo<ThemeContextValue>(
    () => ({ theme, setTheme, resolvedTheme }),
    [theme, setTheme, resolvedTheme],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}
