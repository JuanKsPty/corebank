import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Hands a fetched file to the browser as a download.
 *
 * Needed because the file arrives as a blob rather than as a navigation: the session
 * is a bearer token in a header, so the download had to be fetched rather than linked
 * to, and a blob has to be turned back into something the browser will save.
 *
 * The object URL is revoked on the next tick and not immediately. Revoking it in the
 * same task as the click races the browser's own read of it in some engines, and the
 * failure mode is a download that silently produces an empty file.
 */
export function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)

  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()

  setTimeout(() => URL.revokeObjectURL(url), 0)
}
