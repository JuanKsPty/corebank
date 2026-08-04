/**
 * The logotype.
 *
 * Its own module rather than living in App: the shell needs it and App needs the
 * shell, so keeping it there made the router and the chrome import each other. ESM
 * tolerates that cycle; it is still the wrong shape.
 *
 * Drawn as SVG rather than set as text so the two-tone rule under the name stays
 * locked to the letterforms at any size. The rule is the device a ledger closes a
 * column with, and its copper half is the same accent the active section uses.
 */
export function Wordmark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 108 20" className={className} role="img" aria-label="corebank">
      <text
        x="0"
        y="13.5"
        fill="currentColor"
        fontFamily="Archivo, sans-serif"
        fontSize="15"
        fontWeight="700"
        // The width axis, the same one the display type uses.
        fontStretch="118%"
        letterSpacing="-0.5"
      >
        corebank
      </text>
      <path d="M0 18h54" stroke="currentColor" strokeWidth="1.4" opacity="0.45" />
      <path d="M54 18h54" stroke="#A8531F" strokeWidth="1.4" />
    </svg>
  )
}
