import { useEffect, useState } from 'react'

/**
 * True below the same `md` breakpoint (768px) the shell's chrome already
 * switches on between `DesktopNav` and `MobileAppBar` — one width, so a
 * screen the rest of the app treats as a phone is never treated as a desktop
 * here.
 *
 * Initialised synchronously from `matchMedia` rather than starting `false`
 * and correcting itself after mount. `AppShell.tsx` documents exactly this
 * failure mode for its own chrome — a naive mobile check reports `false` on
 * the first render even on an actual phone — and rebuilt the shell around
 * CSS instead of fixing the hook. A CSS-only approach does not fit here: a
 * page that decides once whether to even ask `/api/auth/device` (SignInPage)
 * cannot correct a wrong first guess the way toggling a class can.
 */
const QUERY = '(max-width: 767px)'

export function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(() => window.matchMedia(QUERY).matches)

  useEffect(() => {
    const mql = window.matchMedia(QUERY)
    const onChange = () => setIsMobile(mql.matches)
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [])

  return isMobile
}
