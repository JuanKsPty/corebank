/**
 * The layout ladder.
 *
 * Every horizontal gutter and every width cap in the application comes from here, so a
 * page cannot drift out of step with the chrome around it.
 *
 * This has been wrong twice in opposite directions, and both are worth recording.
 *
 * It began as `mx-auto max-w-[1180px] px-5` declared three times in the shell with no
 * breakpoint variants at all, which capped the content at 1140px for ever: a 1920px
 * display showed 390px of empty paper down each side, and the transfer form — which
 * nested a second `max-w-2xl` inside the first — was 672px of form in the middle of
 * 1440px of nothing.
 *
 * The correction removed the cap entirely and let the content run to the full viewport.
 * That fixed the margins and created a worse problem: an application with three
 * destinations and a handful of cards does not have enough to say to fill 1824px, so the
 * dead space moved from the edges into the layout itself — a statement whose description
 * column was 1700px wide, an account grid five across holding one card. Space at the
 * edges reads as a page; the same space distributed between elements reads as an
 * unfinished one.
 *
 * So the cap is back, generous rather than timid, and the distinction that matters is
 * kept: width is governed per block by what the block contains.
 *
 *   - A statement, an account grid or a chart is a DATA surface, and takes the width the
 *     shell gives it.
 *   - A paragraph or a form is a READING surface. A 1000px line of text is unreadable no
 *     matter how much room there is, so these carry `measure` and the space left over is
 *     put to work beside them instead of becoming padding.
 */

/**
 * The shell container: centred, capped, with a gutter that widens as the display does.
 *
 * 88rem is 1408px — comfortably wider than the 1140px it replaces, and narrow enough
 * that the page stays dense. With the assistant column taking 22rem of it from `xl`, the
 * page itself lands around 950px, which is a readable working width rather than a
 * stretched one.
 */
export const shell = 'mx-auto w-full max-w-[88rem] px-4 md:px-6 xl:px-8'

/**
 * The gutter alone, for chrome that spans the full viewport rather than the container —
 * the phone's app bar and tab bar, and the public pages, which set their own width.
 */
export const gutter = 'px-4 md:px-6 xl:px-8'

/**
 * The cap for a reading surface: prose, a form, an empty state's explanation.
 *
 * 68 characters at the body size, which is inside the 45–75 that typographers settle on
 * for a comfortable line.
 */
export const measure = 'max-w-[34rem]'
