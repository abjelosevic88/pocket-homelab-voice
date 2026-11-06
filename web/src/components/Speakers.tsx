// Who's who in a meeting: each voice on the call with how long it talked, a
// clip to hear it, and its name — recognised by voice, asked about ("Is this
// Alex?"), heard in the conversation, or typed in. Every name given or
// confirmed teaches Voice that voice for the next meeting.
import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Memo, Person, Speaker } from '../lib/types'
import { clock } from '../lib/voice'
import { speakerSpans, voices, within, type Range, type Voice } from '../lib/part'
import { Icon } from './icons'
import { MenuButton, errMsg, useToast, type MenuItem } from './ui'

let peopleCache: Person[] | null = null

function usePeople(): Person[] {
  const [people, setPeople] = useState<Person[]>(peopleCache || [])
  useEffect(() => {
    api.get<{ items: Person[] }>(`${V1}/people`).then(r => { peopleCache = r.items; setPeople(r.items) }).catch(() => {})
  }, [])
  return people
}

export function WhoCard({ memo: m, onChange, onPlay }: { memo: Memo; onChange: (m: Memo) => void; onPlay: (r: Range) => void }) {
  const { toast } = useToast()
  const navigate = useNavigate()
  const people = usePeople()
  const [busy, setBusy] = useState('')
  const vs = voices(m).filter(v => v.key !== 'You')
  // talk time as the player counts it: from the words
  const spans = speakerSpans(m.segments)
  const talked = (k: string) => within(spans[k], 0, Infinity)
  const total = voices(m).reduce((a, v) => a + talked(v.key), 0) || 1

  const name = async (key: string, body: Record<string, unknown>, done?: string) => {
    setBusy(key)
    try {
      onChange(await api.put<Memo>(`${V1}/memos/${m.id}/speakers/${key}`, body))
      peopleCache = null
      if (done) toast(done)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  const find = async () => {
    try { onChange(await api.post<Memo>(`${V1}/memos/${m.id}/speakers/find`)) } catch (e) { toast(errMsg(e), 'err') }
  }

  if (m.speakers_status === 'working') {
    return (
      <section className="card who-card">
        <div className="who-wait"><span className="who-pulse"><Icon name="users" size={16} /></span>
          <div><b>Telling the voices on the call apart…</b><p className="muted small">About five minutes for an hour of meeting, gently in the background. You can leave this page.</p></div></div>
      </section>
    )
  }
  if (!m.speakers.length) {
    if (m.status !== 'done' || !m.has_audio) return null
    return (
      <section className="card who-card">
        <div className="who-wait"><span className="who-pulse still"><Icon name="users" size={16} /></span>
          <div className="grow"><b>Who spoke?</b>
            <p className="muted small">Voice can tell the people on the call apart, here on your server, and learn their voices once you name them: the next meeting knows them.</p>
            {m.speakers_status === 'failed' && <p className="bad small">{m.speakers_error}</p>}</div>
          <button type="button" className="btn" onClick={find}><Icon name="users" size={15} />{m.speakers_status === 'failed' ? 'Try again' : 'Find who spoke'}</button></div>
      </section>
    )
  }
  const named = vs.filter(v => v.speaker?.state === 'confirmed').length
  const menu = (): MenuItem[] => [
    { label: 'Tell the voices apart again', icon: 'refresh', onSelect: find },
    { label: 'People and their voices', icon: 'users', onSelect: () => navigate('/people') },
  ]
  return (
    <section className="card who-card">
      <div className="card-head who-head">
        <h3><Icon name="users" size={15} />Who's who</h3>
        <span className="muted small">{vs.length === 1 ? '1 voice on the call' : `${vs.length} voices on the call`}{named > 0 && ` · ${named} named by you`}</span>
        <span className="spacer" />
        <MenuButton label="More" align="right" width={250} items={menu}><Icon name="more" size={18} /></MenuButton>
      </div>
      <ul className="who-list">
        {vs.map(v => <WhoRow key={v.key} v={v} sp={v.speaker!} ms={talked(v.key)} share={talked(v.key) / total} people={people} busy={busy === v.key}
          onPlay={() => onPlay({ from: v.speaker!.sample[0], to: v.speaker!.sample[1] })}
          onName={(body, done) => name(v.key, body, done)} />)}
      </ul>
      <p className="muted small who-foot">Names you give or confirm teach Voice their voices, kept only on your server. <Link to="/people">People</Link></p>
    </section>
  )
}

function WhoRow({ v, sp, ms, share, people, busy, onPlay, onName }: {
  v: Voice; sp: Speaker; ms: number; share: number; people: Person[]; busy: boolean
  onPlay: () => void; onName: (body: Record<string, unknown>, done?: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const typed = (n: string) => { setEditing(false); onName({ name: n }, n ? `${n}: learning their voice` : undefined) }
  const ask = sp.state === 'unknown' || editing
  return (
    <li className={`who-row s-${sp.state}`} style={{ '--c': v.color } as React.CSSProperties}>
      <button type="button" className="who-play" onClick={onPlay} aria-label={`Hear ${v.label}`} title="Hear them"><Icon name="play" size={13} strokeWidth={2.4} /></button>
      <span className="who-main">
        <span className="who-name"><i className="who-dot" />{v.label}
          {sp.state === 'auto' && <span className="pill accent" title={`The voice is ${Math.round((sp.score || 0) * 100)}% like ${sp.name}'s`}>recognised</span>}
          {sp.state === 'confirmed' && <Icon name="check" size={13} className="ok-ico" />}
        </span>
        <span className="muted small tnum">{clock(ms)} · {Math.round(share * 100)}% of the talk</span>
        {sp.hint && (sp.state === 'unknown' || sp.state === 'suggested') && <span className="muted small who-hint" title={sp.hint_why}>Heard as “{sp.hint}”{sp.hint_why ? `: ${sp.hint_why}` : ''}</span>}
      </span>
      <span className="who-act">
        {busy ? <span className="muted small">Saving…</span>
          : ask ? <NameField people={people} hint={sp.hint} initial={editing ? sp.name : ''} onSave={typed} onCancel={editing ? () => setEditing(false) : undefined} />
            : sp.state === 'suggested' ? <>
              <span className="who-q">Is this <b>{sp.suggest}</b>?</span>
              <button type="button" className="btn sm" onClick={() => onName({ answer: 'yes' }, `${sp.suggest}: learning their voice`)}><Icon name="check" size={14} />Yes</button>
              <button type="button" className="btn ghost sm" onClick={() => onName({ answer: 'no' })}>No</button>
              <button type="button" className="btn ghost sm" onClick={() => setEditing(true)}>Someone else</button>
            </>
              : sp.state === 'auto' ? <>
                <button type="button" className="btn sm" onClick={() => onName({ answer: 'yes' })} title={`Yes, it is ${sp.name}: their voice gets better known`}><Icon name="check" size={14} />Right</button>
                <button type="button" className="btn ghost sm" onClick={() => onName({ answer: 'no' })}>Not {sp.name}</button>
              </>
                : <button type="button" className="btn ghost icon-only sm" onClick={() => setEditing(true)} aria-label="Change the name" title="Change the name"><Icon name="edit" size={14} /></button>}
      </span>
    </li>
  )
}

function NameField({ people, hint, initial, onSave, onCancel }: { people: Person[]; hint?: string; initial: string; onSave: (n: string) => void; onCancel?: () => void }) {
  const [v, setV] = useState(initial)
  const listId = `people-pick-${useState(() => Math.random().toString(36).slice(2))[0]}`
  const go = () => { if (v.trim() || initial) onSave(v.trim()) }
  return (
    <span className="who-field">
      {hint && !v && <button type="button" className="btn sm who-hint-btn" onClick={() => onSave(hint)} title={`Name them ${hint}`}><Icon name="sparkles" size={13} />{hint}?</button>}
      <input value={v} onChange={e => setV(e.target.value)} placeholder="Who is this?" list={listId} maxLength={60} autoFocus={!!onCancel} aria-label="Name"
        onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); go() } if (e.key === 'Escape' && onCancel) { e.preventDefault(); e.stopPropagation(); onCancel() } }} />
      <datalist id={listId}>{people.map(p => <option key={p.id} value={p.name} />)}</datalist>
      <button type="button" className="btn primary sm" onClick={go} disabled={!v.trim() && !initial}>Save</button>
      {onCancel && <button type="button" className="btn ghost sm" onClick={onCancel}>Cancel</button>}
    </span>
  )
}
