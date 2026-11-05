// Adding memos without the recorder: recordings you already have (Apple
// Voice Memos, a phone's recorder app, WhatsApp voice notes) and typed text.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import type { Memo } from '../lib/types'
import { fmtBytes } from '../lib/format'
import { KINDS } from '../lib/voice'
import { sendMemo } from './Recorder'
import { Icon } from './icons'
import { Field, Modal, Segmented, useToast } from './ui'

/** "20240105 101500.m4a" (Apple Voice Memos) or the file's own date. */
function recordedAt(f: File): string {
  const m = f.name.match(/(20\d\d)[-_.]?(\d\d)[-_.]?(\d\d)[ _T-]?(\d\d)[-_.:]?(\d\d)[-_.:]?(\d\d)?/)
  if (m) {
    const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]), Number(m[6] || 0))
    if (!isNaN(d.getTime()) && d.getTime() < Date.now() + 864e5) return d.toISOString()
  }
  return new Date(f.lastModified || Date.now()).toISOString()
}

export function UploadModal({ onClose, onDone }: { onClose: () => void; onDone: (first?: Memo) => void }) {
  const { toast } = useToast()
  const [files, setFiles] = useState<File[]>([])
  const [done, setDone] = useState<Record<number, number | 'ok' | string>>({})
  const [busy, setBusy] = useState(false)
  const run = async () => {
    setBusy(true)
    let first: Memo | undefined
    let ok = 0
    for (let i = 0; i < files.length; i++) {
      const f = files[i]
      try {
        const m = await sendMemo(f, f.name, { from: 'upload', recorded_at: recordedAt(f) }, p => setDone(d => ({ ...d, [i]: p })))
        first = first || m
        ok++
        setDone(d => ({ ...d, [i]: 'ok' }))
      } catch (e) { setDone(d => ({ ...d, [i]: (e as Error).message })) }
    }
    setBusy(false)
    toast(ok === files.length ? `${ok} ${ok === 1 ? 'recording' : 'recordings'} added; they are written out one after another` : `${ok} of ${files.length} added`, ok === files.length ? 'ok' : 'err')
    if (ok === files.length) onDone(files.length === 1 ? first : undefined)
  }
  return (
    <Modal title="Add recordings" onClose={onClose} size="sm">
      <div className="grid form-grid">
        <p className="muted small">Voice memos from your phone (Apple Voice Memos: share → Save to Files), WhatsApp voice notes, or any audio file: m4a, mp3, wav, ogg, opus, webm, flac. Each one becomes a memo with the day it was recorded, and is written out on your server.</p>
        <label className="btn"><Icon name="upload" size={15} />Choose files…<input type="file" multiple accept="audio/*,video/*,.m4a,.opus,.ogg,.amr" hidden onChange={e => { setFiles(Array.from(e.target.files || [])); setDone({}) }} /></label>
        {files.length > 0 && <ul className="up-list">{files.map((f, i) => {
          const st = done[i]
          return <li key={i}><Icon name={st === 'ok' ? 'check-circle' : typeof st === 'string' ? 'alert' : 'mic'} size={15} className={st === 'ok' ? 'ok' : typeof st === 'string' ? 'bad' : 'muted'} />
            <span className="grow ellipsis">{f.name}</span><span className="muted small tnum">{typeof st === 'number' ? `${st}%` : typeof st === 'string' && st !== 'ok' ? st : fmtBytes(f.size)}</span></li>
        })}</ul>}
        <div className="form-actions">
          <button type="button" className="btn" onClick={onClose}>Close</button>
          <button type="button" className="btn primary" onClick={run} disabled={busy || !files.length}>{busy ? 'Adding…' : files.length > 1 ? `Add ${files.length} recordings` : 'Add the recording'}</button>
        </div>
      </div>
    </Modal>
  )
}

export function TypeModal({ onClose, onDone }: { onClose: () => void; onDone: (m: Memo) => void }) {
  const { toast } = useToast()
  const [text, setText] = useState('')
  const [kind, setKind] = useState('')
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try { onDone(await api.post<Memo>(`${V1}/memos`, { text, kind })) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Type a memo" onClose={onClose} size="sm">
      <form onSubmit={save} className="grid form-grid">
        <Field label="What to remember" help="Type, paste, or use your keyboard's own dictation. The AI tidies it up and finds tasks and dates in it, like in a recording.">
          <textarea rows={6} value={text} onChange={e => setText(e.target.value)} required autoFocus placeholder="Call the plumber on Monday, and buy light bulbs for the hallway." />
        </Field>
        <Field group label="About"><Segmented size="sm" value={kind} onChange={setKind} options={[{ value: '', label: 'Let AI guess' }, ...KINDS.slice(0, 4).map(k => ({ value: k.id, label: k.label }))]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy || !text.trim()}>Save the memo</button></div>
      </form>
    </Modal>
  )
}
