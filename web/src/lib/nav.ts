import {
  HouseIcon,
  ReceiptTextIcon,
  SparklesIcon,
  WalletIcon,
  type LucideIcon,
} from 'lucide-react'

/**
 * The sections of the signed-in application, declared once.
 *
 * Two surfaces render this list — the desktop navbar and the phone's tab bar — and they
 * must never disagree about what exists or which one is current. The original shell
 * mapped the same array twice inside one file, which worked only because both copies were
 * three lines apart; with the navbar and the tab bar in separate components that
 * coincidence is not available.
 */

export interface NavItem {
  to: string
  /** The label in the rail, which has room for a phrase. */
  label: string
  /** The label under a tab-bar icon, which has room for one word. */
  short: string
  icon: LucideIcon
  /** True for the item whose surface is the assistant rather than a route. */
  assistant?: boolean
}

export const NAV: NavItem[] = [
  { to: '/panel', label: 'Resumen', short: 'Inicio', icon: HouseIcon },
  { to: '/cuentas', label: 'Cuentas', short: 'Cuentas', icon: WalletIcon },
  // The assistant sits in the raised middle slot of the tab bar, because it is the
  // one thing this bank does that the others do not. It has no route: it is a
  // surface that opens over whatever you were looking at, so the balance stays visible
  // behind it while a reservation is confirmed.
  {
    to: '#asistente',
    label: 'Asistente',
    short: 'Asistente',
    icon: SparklesIcon,
    assistant: true,
  },
  { to: '/historial', label: 'Historial', short: 'Historial', icon: ReceiptTextIcon },
]

/** The routed sections, for the desktop navbar. */
export const ROUTES = NAV.filter((item) => !item.assistant)
