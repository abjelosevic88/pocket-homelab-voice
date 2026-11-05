// Sending a memo, or one suggestion from it, to another Pocket app: a note or
// a line in today's note, a task, an event, an expense. Each form starts
// filled in from the memo; nothing is sent until you press the button.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Action, CalendarItem, Memo, MoneyAccount, NoteFolder, Picked, Sent, TaskList } from '../lib/types'
import { APPS } from '../lib/voice'
import { todayISO } from '../lib/format'
import { Icon } from './icons'
import { Select } from './Select'
import { CoreHint, Field, Modal, Segmented, useAsync, useToast } from './ui'

export type Target = 'note' | 'daily' | 'task' | 'event' | 'expense'

export const TARGETS: Record<Target, { app: 'notes' | 'tasks' | 'calendar' | 'money'; label: string; short: string; icon: string; button: string }> = {
  note: { app: 'notes', label: 'Save as a note', short: 'Note', icon: 'note', button: 'Save the note' },
  daily: { app: 'notes', label: "Add to today's note", short: "Today's note", icon: 'journal', button: "Add to today's note" },
  task: { app: 'tasks', label: 'Make a task', short: 'Task', icon: 'check-square', button: 'Add the task' },
  event: { app: 'calendar', label: 'Add to the calendar', short: 'Calendar', icon: 'calendar-plus', button: 'Add the event' },
  expense: { app: 'money', label: 'Book an expense', short: 'Expense', icon: 'wallet', button: 'Book it' },
}

const DONE: Record<Target, string> = { note: 'Saved in Pocket Notes', daily: "Added to today's note", task: 'Added to Pocket Tasks', event: 'Added to your calendar', expense: 'Booked in Pocket Money' }

export const actionTarget = (a: Action): Target => a.type === 'journal' ? 'daily' : a.type

/** Whether the person can send to an app now, and why not. */
export function useCanSend() {
  const { settings } = useApp()
  return (t: Target): { ok: boolean; why: string } => {
    const app = TARGETS[t].app
    if (!settings) return { ok: false, why: '' }
    const coreOn = settings.core_configured && settings.core_registered !== false
    const via = settings[`${app}_via`]
    if (via) return { ok: true, why: '' }
    const mode = settings[`${app}_mode`]
    if (mode === 'off') return { ok: false, why: `${APPS[app].name} is switched off in Settings → Connected apps` }
    if (!coreOn && mode !== 'direct') return { ok: false, why: `Needs Pocket Core, or a direct connection to ${APPS[app].name} in Settings → Connected apps` }
    return { ok: false, why: settings[`${app}_detail`] || `Connect ${APPS[app].name} in Settings → Connected apps` }
  }
}

export function SendModal({ memo, target, action, onClose, onSent }: { memo: Memo; target: Target; action?: number; onClose: () => void; onSent: (m: Memo, s: Sent) => void }) {
  const { settings } = useApp()
  const { toast } = useToast()
  const a = action !== undefined ? memo.actions[action] : undefined
  const words = memo.tidy || memo.transcript
  const [f, setF] = useState(() => ({
    title: a?.title || (target === 'note' ? memo.title : ''),
    text: target === 'daily' ? (a?.text || memo.summary || words) : words,
    which: memo.tidy ? 'tidy' : 'words',
    folder: settings?.notes_folder ?? 'Voice',
    due: a?.due || '', notes: a?.notes || '', list_id: settings?.tasks_list_id || 0,
    start: a?.start || '', end: a?.end || '', location: a?.location || '', calendar_id: settings?.calendar_id || 0,
    amount: a?.amount ? String(a.amount) : '', currency: a?.currency || settings?.currency || '', date: a?.date || todayISO(),
    account_id: settings?.money_account_id || 0,
  }))
  const [busy, setBusy] = useState(false)
  const t = TARGETS[target]
  const set = (k: keyof typeof f, v: string | number) => setF(x => ({ ...x, [k]: v }))
  const allDay = f.start.length <= 10

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      const body: Record<string, unknown> = { to: target, action, title: f.title }
      if (target === 'note') Object.assign(body, { text: f.which === 'tidy' ? memo.tidy : f.which === 'words' ? memo.transcript : f.text, folder: f.folder })
      if (target === 'daily') Object.assign(body, { text: f.text })
      if (target === 'task') Object.assign(body, { due: f.due, notes: f.notes, list_id: f.list_id })
      if (target === 'event') Object.assign(body, { start: f.start, end: f.end, location: f.location, calendar_id: f.calendar_id })
      if (target === 'expense') Object.assign(body, { amount: Number(f.amount.replace(',', '.')), currency: f.currency, date: f.date, account_id: f.account_id })
      const r = await api.post<{ sent: Sent; memo: Memo }>(`${V1}/memos/${memo.id}/send`, body)
      toast(DONE[target])
      onSent(r.memo, r.sent)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }

  return (
    <Modal title={t.label} onClose={onClose} size="sm">
      <form onSubmit={submit} className="grid form-grid">
        {target === 'note' && <>
          <Field label="Title"><input value={f.title} onChange={e => set('title', e.target.value)} required maxLength={160} autoFocus /></Field>
          {memo.tidy && <Field group label="Words"><Segmented size="sm" value={f.which} onChange={v => set('which', v)} options={[{ value: 'tidy', label: 'Tidied up' }, { value: 'words', label: 'As said' }]} /></Field>}
          <FolderField value={f.folder} onChange={v => set('folder', v)} />
          <p className="muted small">The summary goes on top and a link back to the recording at the bottom.</p>
        </>}
        {target === 'daily' && <Field label="Line for today's note" help="Added to your daily note in Pocket Notes with the time."><textarea rows={4} value={f.text} onChange={e => set('text', e.target.value)} required autoFocus /></Field>}
        {target === 'task' && <>
          <Field label="Task"><input value={f.title} onChange={e => set('title', e.target.value)} required maxLength={160} autoFocus placeholder="Call the dentist" /></Field>
          <div className="two-col tight">
            <Field label="Day"><input type="date" value={f.due} onChange={e => set('due', e.target.value)} /></Field>
            <ListField value={f.list_id} onChange={v => set('list_id', v)} />
          </div>
          <Field label="Notes"><textarea rows={2} value={f.notes} onChange={e => set('notes', e.target.value)} /></Field>
        </>}
        {target === 'event' && <>
          <Field label="Event"><input value={f.title} onChange={e => set('title', e.target.value)} required maxLength={160} autoFocus /></Field>
          <Field group label="When"><Segmented size="sm" value={allDay ? 'day' : 'time'} onChange={v => set('start', v === 'day' ? (f.start || todayISO()).slice(0, 10) : `${(f.start || todayISO()).slice(0, 10)}T09:00`)} options={[{ value: 'time', label: 'At a time' }, { value: 'day', label: 'All day' }]} /></Field>
          <div className="two-col tight">
            <Field label="Starts">{allDay ? <input type="date" value={f.start} onChange={e => set('start', e.target.value)} required /> : <input type="datetime-local" value={f.start} onChange={e => set('start', e.target.value)} required />}</Field>
            <Field label="Ends" help="Empty: an hour later.">{allDay ? <input type="date" value={f.end.slice(0, 10)} onChange={e => set('end', e.target.value)} /> : <input type="datetime-local" value={f.end} onChange={e => set('end', e.target.value)} />}</Field>
          </div>
          <Field label="Where"><input value={f.location} onChange={e => set('location', e.target.value)} /></Field>
          <CalendarField value={f.calendar_id} onChange={v => set('calendar_id', v)} />
        </>}
        {target === 'expense' && <>
          <Field label="What for"><input value={f.title} onChange={e => set('title', e.target.value)} required maxLength={160} autoFocus /></Field>
          <div className="two-col tight">
            <Field label="Amount"><input inputMode="decimal" value={f.amount} onChange={e => set('amount', e.target.value)} required placeholder="12.50" /></Field>
            <Field label="Currency" help="Empty: the account's."><input value={f.currency} onChange={e => set('currency', e.target.value.toUpperCase())} maxLength={3} /></Field>
          </div>
          <div className="two-col tight">
            <Field label="Day"><input type="date" value={f.date} onChange={e => set('date', e.target.value)} required /></Field>
            <AccountField value={f.account_id} onChange={v => set('account_id', v)} />
          </div>
        </>}
        <div className="form-actions">
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button className="btn primary" disabled={busy}><Icon name={t.icon} size={15} />{busy ? 'Sending…' : t.button}</button>
        </div>
      </form>
    </Modal>
  )
}

function FolderField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { data } = useAsync(() => api.get<Picked<NoteFolder>>(`${V1}/links/notes/folders`), [])
  const opts = (data?.items || []).map(f => ({ value: f.path, label: f.path }))
  if (value && !opts.some(o => o.value === value)) opts.unshift({ value, label: `${value} (new folder)` })
  return <Field label="Folder"><Select ariaLabel="Folder" value={value} onChange={onChange} options={opts} placeholder="The top of your notes" clearLabel="The top of your notes" onCreate={l => l.trim()} createLabel="New folder" /></Field>
}

function ListField({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  const { data } = useAsync(() => api.get<Picked<TaskList>>(`${V1}/links/tasks/lists`), [])
  return <Field label="List"><Select ariaLabel="List" value={value ? String(value) : ''} onChange={v => onChange(Number(v) || 0)} options={(data?.items || []).map(l => ({ value: String(l.id), label: l.name }))} placeholder="Your inbox" clearLabel="Your inbox" /></Field>
}

function CalendarField({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  const { data } = useAsync(() => api.get<Picked<CalendarItem>>(`${V1}/links/calendar/calendars`), [])
  return <Field label="Calendar"><Select ariaLabel="Calendar" value={value ? String(value) : ''} onChange={v => onChange(Number(v) || 0)} options={(data?.items || []).map(c => ({ value: String(c.id), label: c.name }))} placeholder="My default calendar" clearLabel="My default calendar" /></Field>
}

function AccountField({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  const { data } = useAsync(() => api.get<Picked<MoneyAccount>>(`${V1}/links/money/accounts`), [])
  return <Field label="Account"><Select ariaLabel="Account" value={value ? String(value) : ''} onChange={v => onChange(Number(v) || 0)} options={(data?.items || []).map(a => ({ value: String(a.id), label: a.name, hint: a.currency }))} placeholder="Everyday account" clearLabel="Everyday account" /></Field>
}

/** The send buttons under a memo: each greyed out with why when its app is not connected. */
export function SendButtons({ memo, onSend }: { memo: Memo; onSend: (t: Target) => void }) {
  const can = useCanSend()
  const { settings } = useApp()
  const coreOn = !!settings && settings.core_configured && settings.core_registered !== false
  const none = (['note', 'daily', 'task', 'event', 'expense'] as Target[]).every(t => !can(t).ok)
  const noWords = !(memo.tidy || memo.transcript)
  return (
    <div className="send-bar">
      {(['note', 'daily', 'task', 'event', 'expense'] as Target[]).map(t => {
        const c = can(t)
        const off = !c.ok || ((t === 'note' || t === 'daily') && noWords)
        return <button key={t} type="button" className="btn sm" disabled={off} title={c.ok ? (noWords ? 'There are no words yet' : `${TARGETS[t].label} in ${APPS[TARGETS[t].app].name}`) : c.why} onClick={() => onSend(t)}><Icon name={TARGETS[t].icon} size={14} /><span className="lbl-long">{TARGETS[t].label}</span><span className="lbl-short">{TARGETS[t].short}</span></button>
      })}
      {none && !coreOn && <CoreHint />}
    </div>
  )
}
