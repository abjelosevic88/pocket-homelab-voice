// What memos are about, how long they are, and where they went.
import type { Kind, Memo, Sent } from './types'

export const KINDS: { id: Kind; label: string; plural: string; icon: string }[] = [
  { id: 'note', label: 'Note', plural: 'Notes', icon: 'note' },
  { id: 'todo', label: 'To-do', plural: 'To-dos', icon: 'check-square' },
  { id: 'idea', label: 'Idea', plural: 'Ideas', icon: 'zap' },
  { id: 'journal', label: 'Journal', plural: 'Journal', icon: 'journal' },
  { id: 'meeting', label: 'Meeting', plural: 'Meetings', icon: 'users' },
  { id: 'shopping', label: 'Shopping', plural: 'Shopping', icon: 'cart' },
  { id: 'reminder', label: 'Reminder', plural: 'Reminders', icon: 'bell' },
]

export const kindOf = (k: string) => KINDS.find(x => x.id === k)
export const kindIcon = (k: string) => kindOf(k)?.icon || 'mic'

/** 83 000 ms → "1:23". */
export function clock(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60
  return h ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}` : `${m}:${String(sec).padStart(2, '0')}`
}

/** "2 min", "45 s" — for summaries. */
export function minutes(ms: number): string {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s} s`
  const m = Math.round(s / 60)
  return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`
}

export const APPS: Record<string, { name: string; icon: string }> = {
  notes: { name: 'Pocket Notes', icon: 'note' },
  tasks: { name: 'Pocket Tasks', icon: 'check-square' },
  calendar: { name: 'Pocket Calendar', icon: 'calendar' },
  money: { name: 'Pocket Money', icon: 'wallet' },
}

export function sentLabel(s: Sent): string {
  switch (s.kind) {
    case 'note': return 'Note'
    case 'daily': return "Today's note"
    case 'task': return 'Task'
    case 'event': return 'Event'
    case 'expense': return 'Expense'
  }
  return s.kind
}

/** The words worth showing in a list: the summary, else the start of the words. */
export function preview(m: Memo): string {
  const t = m.summary || m.tidy || m.transcript
  return t.replace(/\s+/g, ' ').trim()
}

export function openSuggestions(m: Memo): number {
  return m.actions.filter(a => !a.sent_to && !a.skipped).length
}

export const statusLine = (m: Memo): string => {
  if (m.status === 'failed') return 'Could not be written out'
  if (m.status === 'queued') return m.queue ? `Waiting (${m.queue} ahead)` : 'Waiting to be written out'
  if (m.status === 'working') {
    const p = m.progress
    if (!p) return 'Writing it out…'
    if (p.stage === 'model') return `Getting the speech model ready${p.pct > 0 ? ` (${p.pct}%)` : '…'}`
    if (p.stage === 'preparing') return 'Getting the recording ready…'
    if (p.stage === 'speakers') return 'Telling the voices on the call apart…'
    return p.pct > 0 ? `Writing it out… ${p.pct}%` : 'Writing it out…'
  }
  return ''
}

export const LANG_NAMES: Record<string, string> = {
  bs: 'Bosnian', hr: 'Croatian', sr: 'Serbian', sl: 'Slovenian', mk: 'Macedonian', en: 'English', de: 'German', fr: 'French', it: 'Italian',
  es: 'Spanish', pt: 'Portuguese', nl: 'Dutch', sv: 'Swedish', no: 'Norwegian', da: 'Danish', pl: 'Polish', cs: 'Czech', sk: 'Slovak',
  hu: 'Hungarian', ro: 'Romanian', bg: 'Bulgarian', el: 'Greek', tr: 'Turkish', ru: 'Russian', uk: 'Ukrainian', ar: 'Arabic', he: 'Hebrew',
  hi: 'Hindi', zh: 'Chinese', ja: 'Japanese', ko: 'Korean',
}
export const langName = (c: string) => LANG_NAMES[c] || c.toUpperCase()

/** The local day (YYYY-MM-DD) of a stored time. */
export function localDay(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
