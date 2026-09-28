// Public HTTP browsers may not expose navigator.clipboard. Keep copying
// available from a user click, and return false when manual selection is needed.
export async function copyText(
  value: string,
  clipboard: Pick<Clipboard, 'writeText'> | undefined = globalThis.navigator?.clipboard,
  fallback: (text: string) => boolean = copyWithSelection,
): Promise<boolean> {
  if (clipboard) {
    try { await clipboard.writeText(value); return true } catch { /* try selection */ }
  }
  try { return fallback(value) } catch { return false }
}

function copyWithSelection(value: string): boolean {
  if (typeof document === 'undefined' || typeof document.execCommand !== 'function') return false
  const previousFocus = document.activeElement
  const selection = document.getSelection()
  const ranges = selection ? Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index).cloneRange()) : []
  const field = document.createElement('textarea')
  field.value = value
  field.readOnly = true
  field.style.position = 'fixed'
  field.style.left = '-9999px'
  field.style.top = '0'
  document.body.appendChild(field)
  try {
    field.focus({ preventScroll: true })
    field.select()
    return document.execCommand('copy')
  } finally {
    field.remove()
    if (previousFocus instanceof HTMLElement) previousFocus.focus({ preventScroll: true })
    if (selection) {
      selection.removeAllRanges()
      for (const range of ranges) selection.addRange(range)
    }
  }
}
