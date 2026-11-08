// The lists of memos: the inbox (not dealt with yet), all of them, starred,
// one kind or one tag — grouped by day, newest first, with search.
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { announceChange, useOnChange } from '../lib/data'
import type { Dictation, DictationPage, Memo, MemoPage } from '../lib/types'
import { fmtDate, fmtISOTime, todayISO, addDays } from '../lib/format'
import { APPS, KINDS, clock, kindIcon, kindOf, localDay, openSuggestions, preview, statusLine } from '../lib/voice'
import { useCrumb, openCreate } from '../components/Layout'
import { Icon } from '../components/icons'
import { Confirm, ContextMenu, Empty, Loading, copyText, useContextMenu, useDebounce, useIsMobile, useToast, type MenuItem } from '../components/ui'
import { SendModal, TARGETS, useCanSend, type Target } from '../components/Send'
import { deleteDictation, dictationFrom, dictationMenu } from '../lib/dictation'

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
  // All memos also lists what was dictated on the Mac, or only that (?type=dictations)
  const withDicts = view === 'all'
  const onlyDicts = withDicts && sp.get('type') === 'dictations'
  const [typed, setTyped] = useState(q)
  const dq = useDebounce(typed, 250)
  useEffect(() => { if (dq !== q) setSp(p => { const n = new URLSearchParams(p); if (dq) n.set('q', dq); else n.delete('q'); return n }, { replace: true }) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  const mobile = useIsMobile()
  const [page, setPage] = useState<MemoPage | null>(null)
  const [limit, setLimit] = useState(60)
  const [err, setErr] = useState('')
  const title = onlyDicts ? 'Dictations' : view === 'kind' ? kindOf(kind)?.plural || kind : view === 'tag' ? `#${tag}` : TITLES[view]
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

  // dictations: their own cursor, merged with the memos by time
  const [dicts, setDicts] = useState<{ items: Dictation[]; next: number; total: number } | null>(null)
  const dictSeq = useRef(0)
  const [dictReload, setDictReload] = useState(0)
  const dictPage = (before = 0) => {
    const p = new URLSearchParams({ limit: '50' })
    if (q) p.set('q', q)
    if (before) p.set('before', String(before))
    return api.get<DictationPage>(`${V1}/dictations?${p}`)
  }
  useEffect(() => {
    const n = ++dictSeq.current
    setDicts(null)
    if (!withDicts) return
    dictPage().then(r => { if (n === dictSeq.current) setDicts({ items: r.items, next: r.next || 0, total: r.total }) })
      .catch(() => { if (n === dictSeq.current) setDicts({ items: [], next: 0, total: 0 }) })
  }, [withDicts, q, dictReload]) // eslint-disable-line react-hooks/exhaustive-deps
  const moreDicts = () => {
    if (!dicts?.next) return
    const n = dictSeq.current
    dictPage(dicts.next).then(r => { if (n === dictSeq.current) setDicts(d => d && { items: [...d.items, ...r.items], next: r.next || 0, total: r.total }) }).catch(() => {})
  }

  const memoItems = onlyDicts ? [] : page?.items || []
  const memoMore = !onlyDicts && !!page && page.total > page.items.length
  const dictItems = withDicts ? dicts?.items || [] : []
  const dictMore = withDicts && !!dicts?.next
  const ready = !!page && (!withDicts || !!dicts)
  const groups = useMemo(() => {
    // while one list has more to load, show only down to its oldest loaded
    // item, so the other one's older items never come out of order
    let cutoff = -Infinity
    if (memoMore && memoItems.length) cutoff = Math.max(cutoff, Date.parse(memoItems[memoItems.length - 1].recorded_at))
    if (dictMore && dictItems.length) cutoff = Math.max(cutoff, Date.parse(dictItems[dictItems.length - 1].dictated_at))
    const entries: Entry[] = [
      ...memoItems.map(m => ({ at: Date.parse(m.recorded_at), iso: m.recorded_at, memo: m })),
      ...dictItems.map(d => ({ at: Date.parse(d.dictated_at), iso: d.dictated_at, dict: d })),
    ].filter(e => e.at >= cutoff).sort((a, b) => b.at - a.at)
    const out: { day: string; items: Entry[] }[] = []
    for (const e of entries) {
      const d = localDay(e.iso)
      if (!out.length || out[out.length - 1].day !== d) out.push({ day: d, items: [] })
      out[out.length - 1].items.push(e)
    }
    return out
  }, [memoItems, dictItems, memoMore, dictMore])
  const shown = groups.reduce((n, g) => n + g.items.length, 0)

  return (
    <div className="stack loose">
      <div className="page-head">
        <div className="grow">
          <h1 className="page-title">{title}{onlyDicts ? dicts && <span className="count muted">{dicts.total}</span> : page && <span className="count muted">{page.total}</span>}</h1>
          {view === 'inbox' && <p className="muted small">Memos you have not dealt with yet. Sending one somewhere, or ticking it, takes it out of here.</p>}
        </div>
        {!mobile && <div className="row">
          <button type="button" className="btn" onClick={() => openCreate('upload')}><Icon name="upload" size={15} />Add recordings</button>
          <button type="button" className="btn primary" onClick={() => openCreate('record')}><Icon name="mic" size={15} />Record</button>
        </div>}
      </div>
      <div className="row wrap filters">
        <div className="search-in input-icon"><Icon name="search" size={15} /><input type="search" value={typed} onChange={e => setTyped(e.target.value)} placeholder="Search the words…" aria-label="Search the words" /></div>
        {view !== 'kind' && <div className="chips scroll">
          <Link className={`chip ${onlyDicts ? 'active' : ''}`} to={onlyDicts ? `/memos${q ? `?q=${encodeURIComponent(q)}` : ''}` : `/memos?type=dictations${q ? `&q=${encodeURIComponent(q)}` : ''}`} aria-pressed={onlyDicts}><Icon name="dictation" size={13} />Dictations</Link>
          {KINDS.map(k => <Link key={k.id} className="chip" to={`/kind/${k.id}`}><Icon name={k.icon} size={13} />{k.plural}</Link>)}
        </div>}
      </div>
      {view === 'inbox' && !q && <DictatedToday />}
      {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
      {!ready && !err && <Loading />}
      {ready && page && shown === 0 && (q ? <Empty icon="search" title="Nothing found">{withDicts ? 'No memo or dictation has these words.' : 'No memo has these words.'}</Empty>
        : onlyDicts ? <Empty icon="dictation" title="No dictations yet">What you dictate with Pocket Launcher on your Mac or in Pocket AI shows up here.</Empty>
        : view === 'inbox' ? <InboxEmpty all={page.counts.all} /> : <Empty icon="mic" title="No memos here" />)}
      {ready && groups.map(g => (
        <section key={g.day} className="card flush">
          <h3 className="day-h">{dayLabel(g.day)}</h3>
          <ul className="list">{g.items.map(e => 'memo' in e
            ? <li key={'m' + e.memo.id}><MemoRow memo={e.memo} inbox={view === 'inbox'} onChanged={load} /></li>
            : <li key={'d' + e.dict.id}><DictationRow d={e.dict} onDeleted={() => setDicts(x => x && { ...x, items: x.items.filter(y => y.id !== e.dict.id), total: Math.max(0, x.total - 1) })} onRestored={() => setDictReload(n => n + 1)} /></li>)}</ul>
        </section>
      ))}
      {ready && (memoMore || dictMore) && <div className="row center"><button type="button" className="btn" onClick={() => { if (memoMore) setLimit(l => l + 60); if (dictMore) moreDicts() }}>Show more</button></div>}
    </div>
  )
}

type Entry = { at: number; iso: string; memo: Memo } | { at: number; iso: string; dict: Dictation }

/** A dictation among the memos: the words it typed, where to, how long. */
function DictationRow({ d, onDeleted, onRestored }: { d: Dictation; onDeleted: () => void; onRestored: () => void }) {
  const navigate = useNavigate()
  const { toast } = useToast()
  const ctx = useContextMenu()
  const to = `/dictation?id=${d.id}`
  return (<>
    {ctx.at && <ContextMenu at={ctx.at} onClose={ctx.close} items={dictationMenu(d, toast, () => deleteDictation(d, toast, onDeleted, onRestored), () => navigate(to))} />}
    <a href={to} className={`list-row memo-row dict-mrow ${ctx.at ? 'ctx-on' : ''}`} onContextMenu={ctx.open} onClick={e => { if (e.metaKey || e.ctrlKey) return; e.preventDefault(); navigate(to) }}>
      <span className="list-ico dict-ico"><Icon name="dictation" size={16} /></span>
      <span className="grow row-main">
        <span className="dict-mtitle">{d.text}</span>
        <span className="dict-msub muted small">Dictation{d.app && <> → {d.app}</>} · {dictationFrom(d)} · <span className="tnum">{clock(d.duration_ms)}</span>{d.polished && <span className="pill accent">polished</span>}</span>
      </span>
      <span className="row-side"><span className="muted small tnum">{fmtISOTime(d.dictated_at)}</span></span>
    </a>
  </>)
}

/** On top of the inbox: what was dictated on the Mac today, one line. */
function DictatedToday() {
  const [today, setToday] = useState<DictationPage | null>(null)
  const load = () => { const d = todayISO(); api.get<DictationPage>(`${V1}/dictations?from=${d}&to=${d}&limit=1`).then(setToday).catch(() => {}) }
  useEffect(load, [])
  useOnChange(load)
  const last = today?.items[0]
  if (!today || !today.total || !last) return null
  const words = last.text.split(/\s+/)
  const start = words.slice(0, 8).join(' ') + (words.length > 8 ? '…' : '')
  return (
    <Link to="/dictation" className="card dict-strip">
      <span className="list-ico dict-ico"><Icon name="dictation" size={15} /></span>
      <span className="grow ellipsis"><b>Dictated today</b> · <span className="tnum">{today.total}</span> · <span className="muted">last: “{start}”{last.app && <> → {last.app}</>}</span></span>
      <Icon name="chevron-right" size={15} className="muted" />
    </Link>
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
  const can = useCanSend()
  const ctx = useContextMenu()
  const [send, setSend] = useState<Target | null>(null)
  const [del, setDel] = useState(false)
  const status = statusLine(m)
  const sugg = openSuggestions(m)
  const changed = () => { onChanged?.(); announceChange() }
  const setFiled = async (filed: boolean) => {
    try { await api.put(`${V1}/memos/${m.id}`, { filed }); toast(filed ? 'Out of the inbox' : 'Back in the inbox', 'ok', { label: 'Undo', run: async () => { await api.put(`${V1}/memos/${m.id}`, { filed: !filed }); announceChange() } }); changed() } catch (er) { toast((er as Error).message, 'err') }
  }
  const put = async (body: Partial<Memo>, done?: string) => {
    try { await api.put(`${V1}/memos/${m.id}`, body); if (done) toast(done); changed() } catch (er) { toast((er as Error).message, 'err') }
  }
  const post = async (path: string, done: string) => {
    try { await api.post(`${V1}/memos/${m.id}/${path}`, {}); toast(done); changed() } catch (er) { toast((er as Error).message, 'err') }
  }
  const file = (e: React.MouseEvent) => { e.preventDefault(); e.stopPropagation(); setFiled(!m.filed) }
  const words = m.tidy || m.transcript
  const busy = m.status === 'queued' || m.status === 'working' || m.ai_status === 'working'
  const menu = (): MenuItem[] => [
    { label: m.filed ? 'Back to inbox' : 'Mark as done', icon: m.filed ? 'inbox' : 'check', onSelect: () => setFiled(!m.filed) },
    { label: m.starred ? 'Unstar' : 'Star', icon: 'star', onSelect: () => put({ starred: !m.starred }, m.starred ? 'Unstarred' : 'Starred') },
    { label: 'Open', icon: 'chevron-right', onSelect: () => navigate(`/memos/${m.id}`) },
    { label: 'Send to…', icon: 'send', submenu: (['note', 'daily', 'task', 'event', 'expense'] as Target[]).map(t => ({ label: TARGETS[t].label, icon: TARGETS[t].icon, disabled: !can(t).ok || ((t === 'note' || t === 'daily') && !words), onSelect: () => setSend(t) })) },
    { label: 'Copy text', icon: 'copy', disabled: !words, onSelect: async () => toast(await copyText(words) ? 'Copied' : 'Could not copy', 'ok') },
    ...(m.status === 'failed' && m.has_audio ? [{ label: 'Write out again', icon: 'refresh', onSelect: () => post('retry', 'Written out again soon') }] : []),
    ...(m.status === 'done' && m.transcript && (!m.tidy || m.ai_status === 'failed') && !busy ? [{ label: m.tidy ? 'Tidy up again' : 'Tidy up with AI', icon: 'sparkles', onSelect: () => post('tidy', 'Tidied up') }] : []),
    { label: 'Change kind', icon: kindIcon(m.kind), submenu: KINDS.map(k => ({ label: k.label, checked: m.kind === k.id, onSelect: () => put({ kind: m.kind === k.id ? '' : k.id }, m.kind === k.id ? 'Kind cleared' : `Now: ${k.label}`) })) },
    'sep',
    { label: 'Delete', icon: 'trash', danger: true, onSelect: () => setDel(true) },
  ]
  return (
    <>
      <a href={`/memos/${m.id}`} className={`list-row memo-row ${ctx.at ? 'ctx-on' : ''}`} onContextMenu={ctx.open} onClick={e => { if (e.metaKey || e.ctrlKey) return; e.preventDefault(); navigate(`/memos/${m.id}`) }}>
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
      {ctx.at && <ContextMenu at={ctx.at} items={menu()} onClose={ctx.close} />}
      {send && <SendModal memo={m} target={send} onClose={() => setSend(null)} onSent={() => { setSend(null); changed() }} />}
      {del && <Confirm title="Delete this memo?" message="The recording and its words are deleted. What you sent to other apps stays there." confirmLabel="Delete" onCancel={() => setDel(false)}
        onConfirm={async () => { setDel(false); try { await api.del(`${V1}/memos/${m.id}`); toast('Deleted'); changed() } catch (er) { toast((er as Error).message, 'err') } }} />}
    </>
  )
}
