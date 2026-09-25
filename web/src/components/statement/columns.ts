/**
 * The statement's columns, declared once.
 *
 * This file exists because of a specific defect. The counterparty column was
 * authored as `<th className="w-[8rem]">` in the header row and as
 * `<td className="whitespace-nowrap">` in the body — two places, no relationship
 * between them — so nothing connected the 104px the column actually offered to the
 * 134px its content needed, and the overflow was invisible in the source. Widths
 * now live on `<col>` elements generated from this table and the cells carry none,
 * which makes that class of disagreement structurally impossible rather than merely
 * fixed.
 *
 * `table-layout: fixed` means these widths are authoritative: a cell can never
 * widen its column, so every column must be declared wide enough for its content or
 * be explicitly allowed to clip. The arithmetic for each is in its comment, in
 * Spline Sans Mono units — 1200/2000 em, i.e. exactly 0.600em per glyph, measured
 * from the woff2 rather than assumed.
 */

export type ColumnKey = 'day' | 'concept' | 'counterparty' | 'debit' | 'credit'

export interface Column {
  key: ColumnKey
  /** The heading. */
  label: string
  /**
   * Width utilities, applied to a `<col>`. `undefined` means the column absorbs
   * whatever the fixed ones leave over.
   */
  width?: string
  /** Text alignment, applied to both the heading and the cells. */
  align?: 'left' | 'right'
  /** True for the column that carries the rule dividing the two sides. */
  dividesSides?: boolean
}

/**
 * Every column here is present at every width, and that is a deliberate constraint
 * rather than a missing feature.
 *
 * A column that appeared only above a breakpoint was tried and removed. `<col>` does
 * not honour `display: none` — a colgroup is a positional list, so hiding the sixth
 * `<col>` while the rows rendered five cells shifted every width one place to the
 * left, and the status badge in the concept cell began painting 34px over the Cargo
 * column: the same defect as the original bug, reintroduced by the fix for it. With
 * `table-layout: fixed` the colgroup and the cells must agree on how many columns
 * exist, at every width, or the widths land on the wrong columns. Responsive
 * behaviour belongs inside a cell, never in the count of them.
 */

export const COLUMNS: Column[] = [
  {
    key: 'day',
    label: 'Día',
    // 3.25rem = 52px, less 24px of cell padding = 28px. The content is the day of
    // the month alone — two glyphs, 14px — because the month is already stated by
    // the heading above the group. The column this replaces held "25 sept 2024",
    // twelve glyphs needing 86px in a 68px box, and overflowed for one month a year.
    width: 'w-[3.25rem]',
  },
  {
    key: 'concept',
    label: 'Concepto',
    // Unset, so this column takes every pixel the others do not. On a wide display
    // that is the point: a description may be up to 200 characters and this is the
    // column where the extra width buys something, namely not truncating it.
  },
  {
    key: 'counterparty',
    label: 'Contraparte',
    // 7rem = 112px, less 24px = 88px, holding "···1300" — 7 glyphs ≈ 50px, so 38px
    // of slack. At 3xl the column widens to 11rem (152px of content) and the cell
    // shows the whole 19-character number instead, which is the one piece of
    // information the elision costs and the width to hold it is 137px.
    width: 'w-[7rem] 3xl:w-[11rem]',
  },
  {
    key: 'debit',
    label: 'Cargo',
    // 7.5rem = 120px, less 24px = 96px. A five-figure amount such as
    // "$32,354.53" is 10 glyphs ≈ 72px; six figures would still fit.
    width: 'w-[7.5rem]',
    align: 'right',
    dividesSides: true,
  },
  {
    key: 'credit',
    label: 'Abono',
    width: 'w-[7.5rem]',
    align: 'right',
  },
]
