// The lists of memos: the inbox (not dealt with yet), all of them, starred,
// one kind or one tag — grouped by day, newest first, with search.
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { announceChange, useOnChange } from '../lib/data'
import type { Memo, MemoPage } from '../lib/types'
import { fmtDate, fmtISOTime, todayISO, addDays } from '../lib/format'
import { APPS, KINDS, clock, kindIcon, kindOf, localDay, openSuggestions, preview, statusLine } from '../lib/voice'
import { useCrumb, openCreate } from '../components/Layout'
import { Icon } from '../components/icons'
import { Empty, Loading, useDebounce, useIsMobile, useToast } from '../components/ui'

export type View = 'inbox' | 'all' | 'starred' | 'kind' | 'tag'

const TITLES: Record<View, string> = { inbox: 'Inbox', all: 'All memos', starred: 'Starred', kind: '', tag: '' }

export function dayLabel(day: string): string {
  const t = todayISO()
  if (day === t) return 'Today'
  if (day === addDays(t, -1)) return 'Yesterday'
  return new Date(day + 'T12:00').toLocaleDateString(undefined, { weekday: 'long' }) + ', ' + fmtDate(day, day.slice(0, 4) !== t.slice(0, 4))
}

export default function Memos({ view }: { view: View }) {
  const { kind = '', tag = '' } = useParams()
  const [sp, setSp] = useSearchParams()
  const q = sp.get('q') || ''
  const [typed, setTyped] = useState(q)
  const dq = useDebounce(typed, 250)
  useEffect(() => { if (dq !== q) setSp(dq ? { q: dq } : {}, { replace: true }) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  const mobile = useIsMobile()
  const [page, setPage] = useState<MemoPage | null>(null)
  const [limit, setLimit] = useState(60)
  const [err, setErr] = useState('')
  const title = view === 'kind' ? kindOf(kind)?.plural || kind : view === 'tag' ? `#${tag}` : TITLES[view]
  useCrumb(undefined)

  const url = useMemo(() => {
    const p = new URLSearchParams({ limit: String(limit) })
    if (view === 'inbox') p.set('inbox', '1')
    if (view === 'starred') p.set('starred', '1')
    if (view === 'kind') p.set('kind', kind)
    if (view === 'tag') p.set('tag', tag)
    if (q) p.set('q', q)
    return `${V1}/memos?${p}`
  }, [view, kind, tag, q, limit])

  const load = () => api.get<MemoPage>(url).then(r => { setPage(r); setErr('') }).catch(e => setErr((e as Error).message))
  useEffect(() => { setPage(null); load() }, [url]) // eslint-disable-line react-hooks/exhaustive-deps
  useOnChange(load)
  // while something is being written out, refresh calmly
  const busy = !!page?.items.some(m => m.status === 'queued' || m.status === 'working' || m.ai_status === 'working')
  const loadRef = useRef(load)
  loadRef.current = load
  useEffect(() => {
    if (!busy) return
    const t = window.setInterval(() => { if (!document.hidden) loadRef.current() }, 3000)
    return () => window.clearInterval(t)
  }, [busy])

  const groups = useMemo(() => {
    const out: { day: string; items: Memo[] }[] = []
    for (const m of page?.items || []) {
      const d = localDay(m.recorded_at)
      if (!out.length || out[out.length - 1].day !== d) out.push({ day: d, items: [] })
      out[out.length - 1].items.push(m)
    }
    return out
  }, [page])

  return (
    <div className="stack loose">
      <div className="page-head">
        <div className="grow">
          <h1 className="page-title">{title}{page && <span className="count muted">{page.total}</span>}</h1>
          {view === 'inbox' && <p className="muted small">Memos you have not dealt with yet. Sending one somewhere, or ticking it, takes it out of here.</p>}
        </div>
        {!mobile && <div className="row">
          <button type="button" className="btn" onClick={() => openCreate('upload')}><Icon name="upload" size={15} />Add recordings</button>
          <button type="button" className="btn primary" onClick={() => openCreate('record')}><Icon name="mic" size={15} />Record</button>
        </div>}
      </div>
      <div className="row wrap filters">
        <div className="search-in input-icon"><Icon name="search" size={15} /><input type="search" value={typed} onChange={e => setTyped(e.target.value)} placeholder="Search the words…" aria-label="Search the words" /></div>
        {view !== 'kind' && <div className="chips scroll">{KINDS.map(k => <Link key={k.id} className="chip" to={`/kind/${k.id}`}><Icon name={k.icon} size={13} />{k.plural}</Link>)}</div>}
      </div>
      {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
      {!page && !err && <Loading />}
      {page && page.items.length === 0 && (q ? <Empty icon="search" title="Nothing found">No memo has these words.</Empty>
        : view === 'inbox' ? <InboxEmpty all={page.counts.all} /> : <Empty icon="mic" title="No memos here" />)}
      {groups.map(g => (
        <section key={g.day} className="card flush">
          <h3 className="day-h">{dayLabel(g.day)}</h3>
          <ul className="list">{g.items.map(m => <li key={m.id}><MemoRow memo={m} inbox={view === 'inbox'} onChanged={load} /></li>)}</ul>
        </section>
      ))}
      {page && page.total > page.items.length && <div className="row center"><button type="button" className="btn" onClick={() => setLimit(l => l + 60)}>Show more</button></div>}
    </div>
  )
}

function InboxEmpty({ all }: { all: number }) {
  return (
    <div className="card rec-cta">
      <button type="button" className="rec-big" onClick={() => openCreate('record')} aria-label="Record a memo"><Icon name="mic" size={34} strokeWidth={2} /></button>
      <div>
        <h2>{all ? 'All dealt with' : 'Say it, and it is written down'}</h2>
        <p className="muted">{all ? 'Nothing waits in the inbox. Record the next thing on your mind.' : 'Record a thought, a list or a meeting. It is written out on your own server, tidied up, and the tasks and dates in it go where they belong with one tap.'}</p>
        <div className="row wrap mt">
          <button type="button" className="btn primary" onClick={() => openCreate('record')}><Icon name="mic" size={15} />Record a memo</button>
          <button type="button" className="btn" onClick={() => openCreate('upload')}><Icon name="upload" size={15} />Add recordings</button>
          {all > 0 && <Link className="btn ghost" to="/memos">All memos</Link>}
        </div>
      </div>
    </div>
  )
}

export function MemoRow({ memo: m, inbox, onChanged }: { memo: Memo; inbox?: boolean; onChanged?: () => void }) {
  const navigate = useNavigate()
  const { toast } = useToast()
  const status = statusLine(m)
  const sugg = openSuggestions(m)
  const file = async (e: React.MouseEvent) => {
    e.preventDefault(); e.stopPropagation()
    try { await api.put(`${V1}/memos/${m.id}`, { filed: !m.filed }); toast(m.filed ? 'Back in the inbox' : 'Out of the inbox', 'ok', { label: 'Undo', run: async () => { await api.put(`${V1}/memos/${m.id}`, { filed: m.filed }); announceChange() } }); onChanged?.(); announceChange() } catch (er) { toast((er as Error).message, 'err') }
  }
  return (
    <a href={`/memos/${m.id}`} className="list-row memo-row" onClick={e => { if (e.metaKey || e.ctrlKey) return; e.preventDefault(); navigate(`/memos/${m.id}`) }}>
      <span className={`list-ico kind-ico k-${m.kind || 'none'}`}><Icon name={kindIcon(m.kind)} size={16} /></span>
      <span className="grow row-main">
        <span className="row-title"><span className="ellipsis">{m.title || 'Memo'}</span>{m.starred && <Icon name="star" size={13} className="star-on" />}</span>
        {status ? <span className={`small ${m.status === 'failed' ? 'bad' : 'muted'} ellipsis`}>{status}</span>
          : <span className="muted small ellipsis">{preview(m) || <i>Nothing was said</i>}</span>}
        {(m.sent.length > 0 || sugg > 0) && <span className="row-tags">
          {sugg > 0 && <span className="pill accent"><Icon name="sparkles" size={11} />{sugg} {sugg === 1 ? 'suggestion' : 'suggestions'}</span>}
          {Array.from(new Set(m.sent.map(s => s.app))).map(a => <span key={a} className="pill"><Icon name={APPS[a]?.icon || 'send'} size={11} />{APPS[a]?.name.replace('Pocket ', '') || a}</span>)}
        </span>}
      </span>
      <span className="row-side">
        <span className="tnum small">{clock(m.duration_ms)}</span>
        <span className="muted small tnum">{fmtISOTime(m.recorded_at)}</span>
      </span>
      {inbox && <button type="button" className="btn ghost icon-only sm file-btn" onClick={file} aria-label="Done: out of the inbox" title="Done: out of the inbox"><Icon name="check" size={16} /></button>}
    </a>
  )
}
