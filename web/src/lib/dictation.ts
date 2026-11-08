// What the dictation rows share: their right-click menu and delete with undo.
import { api, V1 } from './api'
import type { Dictation } from './types'
import { copyText, errMsg, type MenuItem } from '../components/ui'

type Toast = (msg: string, kind?: 'ok' | 'err', action?: { label: string; run: () => void }) => void

/** The body that puts a deleted dictation back (same ref, same time). */
export const dictationBody = (d: Dictation) => ({ ref: d.ref, text: d.text, raw: d.raw, app: d.app, app_id: d.app_id, duration_ms: d.duration_ms, engine: d.engine, language: d.language, device: d.device, source: d.source, polished: d.polished, dictated_at: d.dictated_at })

/** Where a dictation was made: the client ("Pocket AI app"), then the device when it adds something. */
export function dictationFrom(d: Dictation): string {
  const src = d.source || 'Pocket Launcher'
  return d.device && !src.toLowerCase().includes(d.device.toLowerCase()) ? `${src} · ${d.device}` : src
}

/** The icon for where a dictation was made: a phone, a browser or a computer. */
export function dictationFromIcon(d: Dictation): string {
  const s = `${d.source} ${d.device}`.toLowerCase()
  if (/iphone|ipad|android|phone|\bapp\b/.test(s) && !s.includes('launcher')) return 'smartphone'
  if (s.includes('web')) return 'globe'
  return 'monitor'
}

/** Delete a dictation, with Undo in the toast. */
export async function deleteDictation(d: Dictation, toast: Toast, onDeleted: () => void, onRestored: () => void) {
  try {
    await api.del(`${V1}/dictations/${d.id}`)
    onDeleted()
    toast('Dictation deleted', 'ok', {
      label: 'Undo', run: async () => {
        try { await api.post(`${V1}/dictations`, dictationBody(d)); onRestored() } catch (e) { toast(errMsg(e), 'err') }
      },
    })
  } catch (e) { toast(errMsg(e), 'err') }
}

/** The right-click menu of a dictation row. */
export function dictationMenu(d: Dictation, toast: Toast, onDelete: () => void, onOpen?: () => void): MenuItem[] {
  const copy = async (t: string) => toast(await copyText(t) ? 'Copied' : 'Could not copy', 'ok')
  return [
    { label: 'Copy text', icon: 'copy', onSelect: () => copy(d.text) },
    ...(d.polished && d.raw ? [{ label: 'Copy as said', icon: 'mic', onSelect: () => copy(d.raw) }] : []),
    ...(onOpen ? [{ label: 'Open in Dictation', icon: 'dictation', onSelect: onOpen }] : []),
    'sep',
    { label: 'Delete', icon: 'trash', danger: true, onSelect: onDelete },
  ]
}
