// Restore removed action focus without moving it away from another record or form.
export function restorePanelFocus(panel: HTMLElement | null, recordId: number) {
  if (!panel || panel.dataset.recordId !== String(recordId)) return
  const active = document.activeElement
  if (active === document.body || (active && panel.contains(active))) panel.focus()
}
