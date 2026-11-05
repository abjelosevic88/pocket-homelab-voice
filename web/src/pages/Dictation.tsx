// Dictation history: what Pocket Launcher on the Mac typed for you by voice,
// grouped by day, with what it adds up to — words, sessions, time saved.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Dictation as DictationT, DictationPage, DictationStats } from '../lib/types'
import { fmtDate, fmtDateTime, plural, todayISO } from '../lib/format'
import { clock, localDay, minutes } from '../lib/voice'
import { useCrumb } from '../components/Layout'
import { Icon } from '../components/icons'
import { Confirm, ContextMenu, Empty, Loading, copyText, errMsg, useContextMenu, useDebounce, useToast } from '../components/ui'
import { deleteDictation, dictationMenu } from '../lib/dictation'
import { dayLabel } from './Memos'

const PAGE = 50

/** 66 210 → "66.2k". */
export function compact(n: number): string {
  if (n < 1000) return n.toLocaleString()
  if (n < 999_500) return (n / 1000).toFixed(n < 99_950 ? 1 : 0).replace(/\.0$/, '') + 'k'
  return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M'
}

/** Seconds saved → "4.9h", "38m", "45s". */
export function savedShort(s: number): string {
  if (s < 60) return `${Math.round(s)}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  return `${(s / 3600).toFixed(1).replace(/\.0$/, '')}h`
}

/** "7h ago", "3m ago", "2d ago". */
function agoShort(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  if (s < 86400 * 30) return `${Math.floor(s / 86400)}d ago`
  return fmtDate(localDay(iso))
}

export default function Dictation() {
  useCrumb(undefined)
  const { toast } = useToast()
  const [sp, setSp] = useSearchParams()
  const q = sp.get('q') || ''
  const app = sp.get('app') || ''
  const [typed, setTyped] = useState(q)
  const dq = useDebounce(typed, 250)
  const setParam = useCallback((k: string, v: string) => setSp(p => { const n = new URLSearchParams(p); if (v) n.set(k, v); else n.delete(k); return n }, { replace: true }), [setSp])
  useEffect(() => { if (dq !== q) setParam('q', dq) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps

  const [stats, setStats] = useState<DictationStats | null>(null)
  const [items, setItems] = useState<DictationT[] | null>(null)
  const [total, setTotal] = useState(0)
  const [next, setNext] = useState(0)
  const [more, setMore] = useState(false)
  const [err, setErr] = useState('')
  const [confirm, setConfirm] = useState(false)
  const seq = useRef(0)
  const paged = useRef(false) // more than the first page is showing

  const query = useCallback((before = 0) => {
    const p = new URLSearchParams({ limit: String(PAGE) })
    if (q) p.set('q', q)
    if (app) p.set('app', app)
    if (before) p.set('before', String(before))
    return api.get<DictationPage>(`${V1}/dictations?${p}`)
  }, [q, app])

  const loadStats = useCallback(() => api.get<DictationStats>(`${V1}/dictations/stats`).then(setStats).catch(() => {}), [])
  const load = useCallback(() => {
    const n = ++seq.current
    return query().then(r => { if (n !== seq.current) return; paged.current = false; setItems(r.items); setTotal(r.total); setNext(r.next || 0); setErr('') })
      .catch(e => { if (n === seq.current) setErr(errMsg(e)) })
  }, [query])
  const loadMore = useCallback(() => {
    if (!next || more) return
    const n = seq.current
    setMore(true)
    query(next).then(r => { if (n !== seq.current) return; paged.current = true; setItems(l => [...(l || []), ...r.items]); setTotal(r.total); setNext(r.next || 0) })
      .catch(e => toast(errMsg(e), 'err')).finally(() => setMore(false))
  }, [next, more, query, toast])

  useEffect(() => { setItems(null); load() }, [load])
  useEffect(() => { loadStats() }, [loadStats])
  // new dictations arrive from the Mac: catch up when the tab comes back
  useEffect(() => {
    const h = () => { if (!document.hidden) { loadStats(); if (!paged.current) load() } }
    document.addEventListener('visibilitychange', h)
    return () => document.removeEventListener('visibilitychange', h)
  }, [load, loadStats])

  // /dictation?id=…: (from All memos or search) bring that one into view and light it up
  const focusId = Number(sp.get('id')) || 0
  const [flash, setFlash] = useState(0)
  const tries = useRef(0)
  useEffect(() => {
    if (!focusId || !items || more) return
    if (items.some(d => d.id === focusId)) {
      setFlash(focusId)
      setParam('id', '')
      requestAnimationFrame(() => document.getElementById(`dict-${focusId}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' }))
      window.setTimeout(() => setFlash(f => (f === focusId ? 0 : f)), 2600)
    } else if (next && tries.current++ < 40) {
      loadMore()
    } else {
      setParam('id', '')
      toast('That dictation is no longer here', 'err')
    }
  }, [focusId, items, next, more]) // eslint-disable-line react-hooks/exhaustive-deps

  // load the next page when the end of the list scrolls into view
  const sentinel = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = sentinel.current
    if (!el || !next) return
    const io = new IntersectionObserver(es => { if (es.some(e => e.isIntersecting)) loadMore() }, { rootMargin: '400px' })
    io.observe(el)
    return () => io.disconnect()
  }, [next, loadMore])

  const groups = useMemo(() => {
    const out: { day: string; items: DictationT[] }[] = []
    for (const d of items || []) {
      const day = localDay(d.dictated_at)
      if (!out.length || out[out.length - 1].day !== day) out.push({ day, items: [] })
      out[out.length - 1].items.push(d)
    }
    return out
  }, [items])

  const remove = (d: DictationT) => deleteDictation(d, toast, () => {
    setItems(l => (l || []).filter(x => x.id !== d.id))
    setTotal(t => Math.max(0, t - 1))
    loadStats()
  }, () => { load(); loadStats() })
  const clear = async () => {
    setConfirm(false)
    try {
      const r = await api.del<{ deleted: number }>(`${V1}/dictations`)
      toast(`${plural(r.deleted, 'dictation')} deleted`)
      load(); loadStats()
    } catch (e) { toast(errMsg(e), 'err') }
  }

  const filtered = !!(q || app)
  const nothingYet = stats && stats.sessions === 0
  const apps = stats?.apps.map(a => a.app) || []
  if (app && !apps.includes(app)) apps.push(app)

  return (
    <div className="stack loose dict-page">
      <div className="page-head">
        <div className="grow">
          <h1 className="page-title">Dictation</h1>
          <p className="muted small">What you dictated on your Mac with Pocket Launcher, the app each one went into, and the time it saved you.</p>
        </div>
      </div>

      {stats && !nothingYet && <StatsCard stats={stats} />}

      {nothingYet && !filtered ? (
        <div className="card"><Empty icon="dictation" title="No dictations yet">
          <p className="dict-empty">Hold the dictation key in Pocket Launcher on your Mac and talk: the words are typed into whatever text box has focus. Each dictation then appears here with the app it went into, how long you spoke and the time it saved you over typing.</p>
        </Empty></div>
      ) : <>
        <div className="row wrap filters">
          <div className="search-in input-icon"><Icon name="search" size={15} /><input type="search" value={typed} onChange={e => setTyped(e.target.value)} placeholder="Search what you dictated…" aria-label="Search what you dictated" /></div>
          {apps.length > 0 && <div className="chips scroll" role="group" aria-label="Filter by app">
            <button type="button" className={`chip ${!app ? 'active' : ''}`} aria-pressed={!app} onClick={() => setParam('app', '')}>All</button>
            {apps.map(a => <button key={a} type="button" className={`chip ${app === a ? 'active' : ''}`} aria-pressed={app === a} onClick={() => setParam('app', app === a ? '' : a)}>{a}</button>)}
          </div>}
        </div>

        {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
        {!items && !err && <Loading />}
        {items && items.length === 0 && <Empty icon="search" title="Nothing found">{q ? 'No dictation has these words.' : 'Nothing was dictated into this app.'}</Empty>}

        {groups.map(g => (
          <section key={g.day} className="card flush">
            <h3 className="day-h dict-day">{dayLabel(g.day)}</h3>
            <ul className="list">{g.items.map(d => <li key={d.id} id={`dict-${d.id}`} className={flash === d.id ? 'dict-flash' : undefined}><DictationRow d={d} onDelete={() => remove(d)} onCopy={async () => toast(await copyText(d.text) ? 'Copied' : 'Could not copy', 'ok')} /></li>)}</ul>
          </section>
        ))}

        {items && items.length > 0 && <div className="dict-foot">
          <span className="muted small tnum">{plural(total, 'item')}</span>
          {next > 0 && <button type="button" className="btn sm" onClick={loadMore} disabled={more}>{more ? 'Loading…' : 'Load more'}</button>}
          <span className="spacer" />
          <button type="button" className="btn ghost sm danger-text" onClick={() => setConfirm(true)}><Icon name="trash" size={14} />Clear history</button>
        </div>}
        <div ref={sentinel} aria-hidden="true" />
      </>}

      {confirm && <Confirm title="Clear dictation history?" confirmLabel="Clear history" onCancel={() => setConfirm(false)} onConfirm={clear}
        message={<>All {plural(stats?.sessions || total, 'dictation')} and the statistics they make up are deleted from this server. What was typed in your apps stays there.</>} />}
    </div>
  )
}

function StatsCard({ stats: s }: { stats: DictationStats }) {
  const max = Math.max(1, ...s.days.map(d => d.words))
  const today = todayISO()
  const line = [
    `Today: ${plural(s.today.words, 'word')}`,
    plural(s.today.sessions, 'session'),
    `saved ${minutes(s.today.saved_seconds * 1000)}`,
  ]
  return (
    <section className="card dict-stats" aria-label="Dictation statistics">
      <div className="dict-nums">
        <div className="dict-num"><span className="dict-l">Words</span><span className="dict-v" title={s.words.toLocaleString()}>{compact(s.words)}</span></div>
        <div className="dict-num"><span className="dict-l">Sessions</span><span className="dict-v" title={s.sessions.toLocaleString()}>{compact(s.sessions)}</span></div>
        <div className="dict-num"><span className="dict-l">Time saved</span><span className="dict-v" title={`${minutes(s.saved_seconds * 1000)} over typing at 40 words a minute`}>{savedShort(s.saved_seconds)}</span></div>
        <div className="dict-chart" role="img" aria-label={`Words per day, last ${s.days.length} days`}>
          <div className="dict-bars">
            {s.days.map(d => (
              <span key={d.date} className={`dict-bar ${d.words ? '' : 'zero'} ${d.date === today ? 'today' : ''}`} style={{ height: d.words ? `${Math.max(6, (d.words / max) * 100)}%` : undefined }}
                title={`${fmtDate(d.date)}: ${plural(d.words, 'word')} · ${plural(d.sessions, 'session')}`} />
            ))}
          </div>
          <div className="dict-axis muted xsmall"><span>{s.days.length} days</span><span>today</span></div>
        </div>
      </div>
      <p className="dict-line muted small">
        {line.join(' · ')}
        {s.streak > 0 && <> · <span className="dict-streak"><Icon name="flame" size={13} />{s.streak}-day streak</span></>}
        {s.wpm > 0 && <> · <span className="tnum">{s.wpm} wpm</span></>}
      </p>
    </section>
  )
}

function DictationRow({ d, onDelete, onCopy }: { d: DictationT; onDelete: () => void; onCopy: () => void }) {
  const { toast } = useToast()
  const ctx = useContextMenu()
  return (<>
    {ctx.at && <ContextMenu at={ctx.at} onClose={ctx.close} items={dictationMenu(d, toast, onDelete)} />}
    <div className={`list-row dict-row ${ctx.at ? 'ctx-on' : ''}`} onContextMenu={e => { if (!window.getSelection()?.toString()) ctx.open(e) }}>
      <div className="grow">
        <p className="dict-text">{d.text}</p>
        <div className="dict-meta">
          <time dateTime={d.dictated_at} title={fmtDateTime(d.dictated_at)}>{agoShort(d.dictated_at)}</time>
          <span className="dict-sep">·</span>
          <span className="inline-ico" title={`Spoke for ${clock(d.duration_ms)}${d.engine ? ` · ${d.engine}` : ''}`}><Icon name="mic" size={12} />{clock(d.duration_ms)}</span>
          {d.app && <><span className="dict-sep">·</span><span className="dict-app" title={d.app_id || undefined}>→ {d.app}</span></>}
          {d.polished && <><span className="dict-sep">·</span>
            <span className="pill accent dict-polished" tabIndex={d.raw ? 0 : undefined} aria-label={d.raw ? `Polished. Before: ${d.raw}` : 'Polished'}>polished
              {d.raw && <span className="dict-tip" role="tooltip"><span className="dict-tip-h">Before polishing</span>{d.raw}</span>}
            </span></>}
        </div>
      </div>
      <div className="dict-actions">
        <button type="button" className="btn ghost icon-only sm" onClick={onCopy} aria-label="Copy" title="Copy"><Icon name="copy" size={15} /></button>
        <button type="button" className="btn ghost icon-only sm dict-del" onClick={onDelete} aria-label="Delete" title="Delete"><Icon name="trash" size={15} /></button>
      </div>
    </div>
  </>)
}
