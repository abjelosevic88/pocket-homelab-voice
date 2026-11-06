// People: the voices you named in meetings. Each meeting they are named or
// recognised in teaches Voice their voice; rename them here, or forget them
// (their voice prints go and the meetings call them "Speaker n" again).
import { useEffect, useMemo, useState } from 'react'
import { api, V1 } from '../lib/api'
import type { Person } from '../lib/types'
import { fmtDateTime, plural } from '../lib/format'
import { clock } from '../lib/voice'
import { useCrumb } from '../components/Layout'
import { Icon } from '../components/icons'
import { Confirm, Empty, Loading, errMsg, useToast } from '../components/ui'

const fold = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()

export default function People() {
  useCrumb(undefined)
  const { toast } = useToast()
  const [items, setItems] = useState<Person[] | null>(null)
  const [why, setWhy] = useState('')
  const [err, setErr] = useState('')
  const [q, setQ] = useState('')
  const [editing, setEditing] = useState(0)
  const [forget, setForget] = useState<Person | null>(null)

  const load = () => api.get<{ items: Person[]; available: boolean; detail: string }>(`${V1}/people`)
    .then(r => { setItems(r.items); setWhy(r.available ? '' : r.detail); setErr('') }).catch(e => setErr(errMsg(e)))
  useEffect(() => { load() }, [])

  const rename = async (p: Person, name: string) => {
    try { await api.put(`${V1}/people/${p.id}`, { name }); setEditing(0); toast(`Renamed in every meeting`); load() } catch (e) { toast(errMsg(e), 'err') }
  }
  const shown = useMemo(() => (items || []).filter(p => !q.trim() || fold(p.name).includes(fold(q.trim()))), [items, q])

  return (
    <div className="stack loose">
      <div className="page-head">
        <div className="grow">
          <h1 className="page-title">People{items && <span className="count muted">{items.length}</span>}</h1>
          <p className="muted small">The voices you named in your meetings. Every meeting you name or confirm them in teaches Voice their voice, so the next one knows them. Voice prints stay on your server.</p>
        </div>
      </div>
      {why && <div className="callout info"><Icon name="info" size={15} /><span>This server cannot tell voices apart yet: {why}.</span></div>}
      {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
      {!items && !err && <Loading />}
      {items && items.length === 0 && <div className="card"><Empty icon="users" title="Nobody named yet">Open a meeting recorded with Pocket Launcher: under the player, “Who's who” lists the voices on the call. Name them there, and Voice recognises them from then on.</Empty></div>}
      {items && items.length > 6 && <div className="row wrap filters">
        <div className="search-in input-icon"><Icon name="search" size={15} /><input type="search" value={q} onChange={e => setQ(e.target.value)} placeholder="Search people…" aria-label="Search people" /></div>
      </div>}
      {shown.length > 0 && <section className="card flush">
        <ul className="list">{shown.map(p => <li key={p.id}>{editing === p.id
          ? <RenameRow p={p} onSave={n => rename(p, n)} onCancel={() => setEditing(0)} />
          : <div className="list-row person-row">
            <span className="person-ava" aria-hidden="true">{p.name.slice(0, 1).toUpperCase()}</span>
            <span className="grow row-main">
              <span className="row-title">{p.name}</span>
              <span className="muted small">{plural(p.meetings, 'meeting')} · heard {clock(p.heard_ms)}{p.last_at && ` · last ${fmtDateTime(p.last_at)}`}</span>
            </span>
            <span className="person-learned" title={`How well Voice knows this voice: ${plural(p.voices, 'voice print')}`}>
              <span className="meter"><span style={{ width: `${Math.max(6, p.learned * 100)}%` }} /></span>
              <span className="muted small">{p.learned >= 0.99 ? 'known well' : p.learned >= 0.5 ? 'getting known' : 'just met'}</span>
            </span>
            <span className="word-actions">
              <button type="button" className="btn ghost icon-only sm" onClick={() => setEditing(p.id)} aria-label="Rename" title="Rename"><Icon name="edit" size={15} /></button>
              <button type="button" className="btn ghost icon-only sm dict-del" onClick={() => setForget(p)} aria-label="Forget" title="Forget them and their voice"><Icon name="trash" size={15} /></button>
            </span>
          </div>}</li>)}</ul>
      </section>}
      {forget && <Confirm title={`Forget ${forget.name}?`} confirmLabel="Forget" onCancel={() => setForget(null)}
        message={`Their voice prints are deleted, and the meetings that named them call that voice “Speaker n” again. Nothing else changes.`}
        onConfirm={async () => { const p = forget; setForget(null); try { await api.del(`${V1}/people/${p.id}`); toast(`${p.name} forgotten`); load() } catch (e) { toast(errMsg(e), 'err') } }} />}
    </div>
  )
}

function RenameRow({ p, onSave, onCancel }: { p: Person; onSave: (n: string) => void; onCancel: () => void }) {
  const [v, setV] = useState(p.name)
  return (
    <div className="list-row word-row editing">
      <input value={v} onChange={e => setV(e.target.value)} maxLength={60} autoFocus aria-label="Name"
        onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); onSave(v) } if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCancel() } }} />
      <button type="button" className="btn primary sm" onClick={() => onSave(v)} disabled={!v.trim()}>Save</button>
      <button type="button" className="btn sm" onClick={onCancel}>Cancel</button>
    </div>
  )
}
