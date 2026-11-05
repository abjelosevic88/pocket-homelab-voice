// One memo: the recording, the words that follow along as it plays, the
// tidy version, what the AI found in it and where it went.
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { announceChange } from '../lib/data'
import type { Action, Memo } from '../lib/types'
import { fmtDate, fmtISOTime } from '../lib/format'
import { APPS, KINDS, clock, kindIcon, langName, localDay, sentLabel, statusLine } from '../lib/voice'
import { useCrumb } from '../components/Layout'
import { Player, type PlayerHandle } from '../components/Player'
import { SendButtons, SendModal, TARGETS, actionTarget, useCanSend, type Target } from '../components/Send'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { Confirm, CoreHint, Loading, MenuButton, Modal, Segmented, copyText, useToast, type MenuItem } from '../components/ui'

export default function MemoPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const [m, setM] = useState<Memo | null>(null)
  const [err, setErr] = useState('')
  const [tab, setTab] = useState<'tidy' | 'words'>('tidy')
  const [time, setTime] = useState(0)
  const [send, setSend] = useState<{ t: Target; action?: number } | null>(null)
  const [del, setDel] = useState(false)
  const [again, setAgain] = useState(false)
  const [editing, setEditing] = useState<'' | 'title' | 'words' | 'tidy'>('')
  const player = useRef<PlayerHandle>(null)
  useCrumb(m?.title || (m ? 'Memo' : undefined))

  const load = () => api.get<Memo>(`${V1}/memos/${id}`).then(x => { setM(x); setErr('') }).catch(e => setErr((e as Error).message))
  useEffect(() => { setM(null); load() }, [id]) // eslint-disable-line react-hooks/exhaustive-deps
  const busy = !!m && (m.status === 'queued' || m.status === 'working' || m.ai_status === 'working')
  const loadRef = useRef(load)
  loadRef.current = load
  useEffect(() => {
    if (!busy) return
    const t = window.setInterval(() => { if (!document.hidden) loadRef.current() }, 2000)
    return () => window.clearInterval(t)
  }, [busy])
  useEffect(() => { if (m && !m.tidy && tab === 'tidy' && m.ai_status !== 'working') setTab('words') }, [m?.tidy, m?.ai_status]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (e.key !== ' ' || t.closest('input, textarea, button, [contenteditable="true"], [role="slider"]') || document.querySelector('.modal-backdrop')) return
      e.preventDefault(); player.current?.toggle()
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])

  if (err) return <div className="callout danger"><Icon name="alert" size={15} /><span>{err === 'not found' ? 'This memo is not here (anymore).' : err}</span></div>
  if (!m) return <Loading />

  const save = async (patch: Partial<Memo>, msg?: string) => {
    try { const x = await api.put<Memo>(`${V1}/memos/${m.id}`, patch); setM(x); if (msg) toast(msg); announceChange() } catch (e) { toast((e as Error).message, 'err') }
  }
  const tidyNow = async () => {
    setM({ ...m, ai_status: 'working' })
    try { setM(await api.post<Memo>(`${V1}/memos/${m.id}/tidy`)); setTab('tidy') } catch (e) { toast((e as Error).message, 'err'); load() }
  }
  const retry = async (language?: string) => {
    try { setM(await api.post<Memo>(`${V1}/memos/${m.id}/retry`, language !== undefined ? { language } : {})); announceChange() } catch (e) { toast((e as Error).message, 'err') }
  }
  const skip = async (i: number, skipped: boolean) => {
    try { setM(await api.post<Memo>(`${V1}/memos/${m.id}/actions/${i}`, { skipped })) } catch (e) { toast((e as Error).message, 'err') }
  }
  const words = m.tidy || m.transcript
  const menu = (): MenuItem[] => [
    { label: m.filed ? 'Back to the inbox' : 'Done: out of the inbox', icon: m.filed ? 'inbox' : 'check', onSelect: () => save({ filed: !m.filed }, m.filed ? 'Back in the inbox' : 'Out of the inbox') },
    { label: 'Copy the words', icon: 'copy', disabled: !words, onSelect: async () => toast(await copyText(words) ? 'Copied' : 'Could not copy', 'ok') },
    { label: 'Download the recording', icon: 'download', disabled: !m.has_audio, onSelect: () => { window.location.href = `${m.audio_url}?download=1` } },
    'sep',
    { label: 'Write it out again…', icon: 'refresh', disabled: !m.has_audio || busy, onSelect: () => setAgain(true) },
    { label: 'Tidy up again with AI', icon: 'sparkles', disabled: !m.transcript || busy, onSelect: tidyNow },
    'sep',
    { label: 'Delete', icon: 'trash', danger: true, onSelect: () => setDel(true) },
  ]
  const status = statusLine(m)

  return (
    <div className="stack loose memo-page">
      <div className="memo-head">
        <span className={`detail-ico kind-ico k-${m.kind || 'none'}`}><Icon name={kindIcon(m.kind)} size={22} /></span>
        <div className="grow">
          {editing === 'title' ? <TitleEdit value={m.title} onDone={v => { setEditing(''); if (v !== m.title) save({ title: v }) }} />
            : <h1 className="memo-title" onClick={() => setEditing('title')} title="Rename">{m.title || 'Memo'}</h1>}
          <p className="muted small meta-line">
            <span>{fmtDate(localDay(m.recorded_at))} · {fmtISOTime(m.recorded_at)}</span>
            <span>· {clock(m.duration_ms)}</span>
            {m.language && <span>· {langName(m.language)}</span>}
            {m.source && m.source !== 'upload' && m.source !== 'import' && <span>· from {APPS[m.source]?.name || `Pocket ${m.source[0].toUpperCase()}${m.source.slice(1)}`}</span>}
          </p>
        </div>
        <div className="row memo-tools">
          <button type="button" className={`btn ghost icon-only ${m.starred ? 'star-on' : ''}`} onClick={() => save({ starred: !m.starred })} aria-label={m.starred ? 'Unstar' : 'Star'} title={m.starred ? 'Starred (the recording is kept)' : 'Star'}><Icon name="star" size={18} /></button>
          <button type="button" className="btn sm" onClick={() => save({ filed: !m.filed }, m.filed ? 'Back in the inbox' : 'Out of the inbox')} title={m.filed ? 'Back to the inbox' : 'Done with it: out of the inbox'}><Icon name={m.filed ? 'inbox' : 'check'} size={15} />{m.filed ? 'To inbox' : 'Done'}</button>
          <MenuButton label="More" align="right" width={240} items={menu}><Icon name="more" size={18} /></MenuButton>
        </div>
      </div>

      {status && <div className={`callout ${m.status === 'failed' ? 'danger' : 'info'} memo-status`}>
        <Icon name={m.status === 'failed' ? 'alert' : 'waves'} size={15} />
        <div className="grow">
          <b>{status}</b>{m.status === 'failed' && m.error && <span>: {m.error}</span>}
          {m.status === 'working' && m.progress && m.progress.pct >= 0 && <div className="bar mt"><span style={{ width: `${m.progress.pct}%` }} /></div>}
          {m.status === 'failed' && <div className="row wrap mt"><button type="button" className="btn sm" onClick={() => retry()}><Icon name="refresh" size={14} />Try again</button><button type="button" className="btn sm ghost" onClick={() => setAgain(true)}>Another language…</button></div>}
        </div>
      </div>}

      <div className="card pad-card"><Player ref={player} memo={m} onTime={setTime} /></div>

      {(m.actions.length > 0 || m.ai_status === 'working') && <Suggestions memo={m} onSend={(t, i) => setSend({ t, action: i })} onSkip={skip} />}

      <section className="card">
        <div className="card-head words-head">
          <Segmented size="sm" value={tab} onChange={setTab} options={[{ value: 'tidy', label: 'Tidied up' }, { value: 'words', label: 'As said' }]} />
          <span className="spacer" />
          {tab === 'words' && m.transcript && editing !== 'words' && <button type="button" className="btn ghost sm" onClick={() => setEditing('words')}><Icon name="edit" size={14} />Correct</button>}
          {tab === 'tidy' && m.tidy && editing !== 'tidy' && <button type="button" className="btn ghost sm" onClick={() => setEditing('tidy')}><Icon name="edit" size={14} />Edit</button>}
        </div>
        <div className="card-body">
          {m.summary && tab === 'tidy' && <p className="memo-summary">{m.summary}</p>}
          {tab === 'tidy' && (editing === 'tidy' ? <TextEdit value={m.tidy} onDone={v => { setEditing(''); if (v !== null && v !== m.tidy) save({ tidy: v }, 'Saved') }} />
            : m.tidy ? <Prose text={m.tidy} /> : <TidyEmpty memo={m} onTidy={tidyNow} />)}
          {tab === 'words' && (editing === 'words' ? <TextEdit value={m.transcript} onDone={v => { setEditing(''); if (v !== null && v !== m.transcript) save({ transcript: v }, 'Saved') }} />
            : <Words memo={m} time={time} onSeek={ms => player.current?.seek(ms, true)} />)}
        </div>
      </section>

      <section className="card">
        <div className="card-head"><h3>Send it where it belongs</h3></div>
        <div className="card-body stack">
          <SendButtons memo={m} onSend={t => setSend({ t })} />
          {m.sent.length > 0 && <ul className="sent-list">{m.sent.map(s => (
            <li key={s.id}><Icon name={APPS[s.app]?.icon || 'send'} size={15} />
              <span className="grow ellipsis">{sentLabel(s)} in {APPS[s.app]?.name || s.app}: {s.url ? <a href={s.url} target="_blank" rel="noreferrer">{s.title}</a> : s.title}</span>
              <span className="muted small">{fmtISOTime(s.created_at)}</span></li>
          ))}</ul>}
        </div>
      </section>

      <MemoFacts memo={m} onSave={save} />

      {send && <SendModal memo={m} target={send.t} action={send.action} onClose={() => setSend(null)} onSent={x => { setM(x); setSend(null); announceChange() }} />}
      {del && <Confirm title="Delete this memo?" message="The recording and its words are deleted. What you sent to other apps stays there." confirmLabel="Delete" onCancel={() => setDel(false)}
        onConfirm={async () => { setDel(false); try { await api.del(`${V1}/memos/${m.id}`); announceChange(); toast('Deleted'); navigate(-1) } catch (e) { toast((e as Error).message, 'err') } }} />}
      {again && <AgainModal memo={m} onClose={() => setAgain(false)} onRun={l => { setAgain(false); retry(l) }} />}
    </div>
  )
}

/** Paragraphs, and "- " lines as a list. */
function Prose({ text }: { text: string }) {
  const blocks: { list: boolean; lines: string[] }[] = []
  for (const line of text.split('\n')) {
    const item = /^\s*[-•*]\s+/.test(line)
    const last = blocks[blocks.length - 1]
    if (!line.trim()) { blocks.push({ list: false, lines: [] }); continue }
    if (last && last.list === item && last.lines.length) last.lines.push(item ? line.replace(/^\s*[-•*]\s+/, '') : line)
    else blocks.push({ list: item, lines: [item ? line.replace(/^\s*[-•*]\s+/, '') : line] })
  }
  const speakerLine = /^(You|Others):\s*/
  return (
    <div className="memo-text">
      {blocks.filter(b => b.lines.length).map((b, i) => b.list
        ? <ul key={i}>{b.lines.map((l, j) => <li key={j}>{l}</li>)}</ul>
        : b.lines.some(l => speakerLine.test(l))
          // a meeting's lines: "You: …" / "Others: …"
          ? <div key={i} className="turns">{b.lines.map((l, j) => {
            const who = l.match(speakerLine)?.[1] || ''
            return <div key={j} className={`turn ${speakerClass(who)}`}>{who && <span className="spk">{who}</span>}<p>{l.replace(speakerLine, '')}</p></div>
          })}</div>
          : <p key={i}>{b.lines.join('\n')}</p>)}
    </div>
  )
}

const speakerClass = (who: string) => (who === 'You' ? 'spk-you' : who ? 'spk-others' : '')

function TitleEdit({ value, onDone }: { value: string; onDone: (v: string) => void }) {
  const [v, setV] = useState(value)
  return <input className="memo-title-input" value={v} autoFocus maxLength={140} onChange={e => setV(e.target.value)} onBlur={() => onDone(v.trim())}
    onKeyDown={e => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur(); if (e.key === 'Escape') onDone(value) }} aria-label="Title" />
}

function TextEdit({ value, onDone }: { value: string; onDone: (v: string | null) => void }) {
  const [v, setV] = useState(value)
  return (
    <div className="stack">
      <textarea className="memo-edit" rows={Math.min(18, Math.max(6, v.split('\n').length + 2))} value={v} onChange={e => setV(e.target.value)} autoFocus />
      <div className="row"><button type="button" className="btn primary sm" onClick={() => onDone(v.trim())}>Save</button><button type="button" className="btn sm" onClick={() => onDone(null)}>Cancel</button></div>
    </div>
  )
}

/** The words as said, sentence by sentence; tap one to hear it. */
function Words({ memo: m, time, onSeek }: { memo: Memo; time: number; onSeek: (ms: number) => void }) {
  if (m.status !== 'done' && !m.transcript) return <p className="muted">The words appear here once the recording is written out.</p>
  if (!m.transcript) return <p className="muted">Nothing was said in this recording.</p>
  if (m.edited || !m.segments.length || !m.has_audio) return <Prose text={m.transcript} />
  const seg = (s: Memo['segments'][number], i: number) => {
    const on = time >= s.s && time < (m.segments[i + 1]?.s ?? s.e + 1500)
    return <span key={i} className={`seg ${on ? 'on' : ''}`} onClick={() => onSeek(s.s)} role="button" tabIndex={0} title={clock(s.s)}
      onKeyDown={e => { if (e.key === 'Enter') onSeek(s.s) }}>{s.t} </span>
  }
  // a meeting: turn by turn, who spoke
  if (m.segments.some(s => s.speaker)) {
    const turns: { speaker: string; items: [Memo['segments'][number], number][] }[] = []
    m.segments.forEach((s, i) => {
      const who = s.speaker || ''
      if (!turns.length || turns[turns.length - 1].speaker !== who) turns.push({ speaker: who, items: [] })
      turns[turns.length - 1].items.push([s, i])
    })
    return (
      <div className="turns">
        {turns.map((t, k) => (
          <div key={k} className={`turn ${speakerClass(t.speaker)}`}>
            <span className="spk">{t.speaker || '—'}</span>
            <p className="memo-words">{t.items.map(([s, i]) => seg(s, i))}</p>
          </div>
        ))}
      </div>
    )
  }
  return (
    <p className="memo-words">
      {m.segments.map((s, i) => {
        const on = time >= s.s && time < (m.segments[i + 1]?.s ?? s.e + 1500)
        return <span key={i} className={`seg ${on ? 'on' : ''}`} onClick={() => onSeek(s.s)} role="button" tabIndex={0} title={clock(s.s)}
          onKeyDown={e => { if (e.key === 'Enter') onSeek(s.s) }}>{s.t} </span>
      })}
    </p>
  )
}

function TidyEmpty({ memo: m, onTidy }: { memo: Memo; onTidy: () => void }) {
  const { settings } = useApp()
  const coreOn = !!settings && settings.core_configured && settings.core_registered !== false
  if (m.ai_status === 'working') return <p className="muted row" style={{ gap: 8 }}><Icon name="sparkles" size={14} />Pocket AI is tidying it up…</p>
  if (!m.transcript) return <p className="muted">Once the words are there, the AI tidies them up: punctuation, paragraphs, no “um”.</p>
  return (
    <div className="stack">
      {m.ai_status === 'failed' && <p className="bad small">{m.ai_error}</p>}
      <p className="muted">The AI can tidy the words up, give the memo a title and a summary, and find the tasks, dates and money in it. Only the words go to the AI, never the recording.</p>
      <div className="row wrap"><button type="button" className="btn" onClick={onTidy}><Icon name="sparkles" size={15} />Tidy up with AI</button>{!coreOn && <CoreHint />}</div>
    </div>
  )
}

const ACTION_ICON: Record<string, string> = { task: 'check-square', event: 'calendar-plus', journal: 'journal', expense: 'wallet' }

function actionLine(a: Action): string {
  switch (a.type) {
    case 'task': return a.due ? `Task · ${fmtDate(a.due, false)}` : 'Task'
    case 'event': return `Event · ${fmtDate(a.start!.slice(0, 10), false)}${a.start!.length > 10 ? ' ' + a.start!.slice(11, 16) : ''}${a.location ? ' · ' + a.location : ''}`
    case 'journal': return "For today's note"
    case 'expense': return `Expense · ${a.amount?.toFixed(2)}${a.currency ? ' ' + a.currency : ''}`
  }
  return ''
}

function Suggestions({ memo: m, onSend, onSkip }: { memo: Memo; onSend: (t: Target, i: number) => void; onSkip: (i: number, skipped: boolean) => void }) {
  const can = useCanSend()
  const [showSkipped, setShowSkipped] = useState(false)
  const shown = m.actions.map((a, i) => ({ a, i })).filter(x => showSkipped || !x.a.skipped)
  const skipped = m.actions.filter(a => a.skipped).length
  return (
    <section className="card suggestions">
      <div className="card-head"><h3><Icon name="sparkles" size={15} />Found in this memo</h3><span className="spacer" />{skipped > 0 && <button type="button" className="btn ghost sm" onClick={() => setShowSkipped(s => !s)}>{showSkipped ? 'Hide skipped' : `${skipped} skipped`}</button>}</div>
      {m.ai_status === 'working' && !m.actions.length && <p className="muted pad">Looking for tasks, dates and money…</p>}
      <ul className="list">{shown.map(({ a, i }) => {
        const t = actionTarget(a)
        const c = can(t)
        return (
          <li key={i} className={`list-row sugg ${a.skipped ? 'is-archived' : ''}`}>
            <span className="list-ico"><Icon name={ACTION_ICON[a.type] || 'sparkles'} size={16} /></span>
            <span className="grow row-main">
              <span className="row-title"><span className="ellipsis">{a.type === 'journal' ? a.text : a.title}</span></span>
              <span className="muted small ellipsis">{actionLine(a)}{a.notes ? ` · ${a.notes}` : ''}</span>
            </span>
            {a.sent_to ? <span className="pill ok"><Icon name="check" size={12} />{a.sent_url ? <a href={a.sent_url} target="_blank" rel="noreferrer">In {APPS[a.sent_to]?.name.replace('Pocket ', '')}</a> : `In ${APPS[a.sent_to]?.name.replace('Pocket ', '') || a.sent_to}`}</span>
              : a.skipped ? <button type="button" className="btn ghost sm" onClick={() => onSkip(i, false)}>Bring back</button>
                : <span className="row sugg-btns">
                  <button type="button" className="btn sm" disabled={!c.ok} title={c.ok ? undefined : c.why} onClick={() => onSend(t, i)}><Icon name={TARGETS[t].icon} size={14} />{t === 'task' ? 'Add task' : t === 'event' ? 'Add event' : t === 'daily' ? 'Add' : 'Book'}</button>
                  <button type="button" className="btn ghost icon-only sm" onClick={() => onSkip(i, true)} aria-label="Skip" title="Skip"><Icon name="x" size={15} /></button>
                </span>}
          </li>
        )
      })}</ul>
    </section>
  )
}

function MemoFacts({ memo: m, onSave }: { memo: Memo; onSave: (p: Partial<Memo>) => void }) {
  const [tags, setTags] = useState(m.tags)
  useEffect(() => setTags(m.tags), [m.tags])
  return (
    <section className="card">
      <div className="card-body facts">
        <label className="fact-row"><span className="muted small">About</span>
          <Select ariaLabel="What it is about" value={m.kind} onChange={v => onSave({ kind: v as Memo['kind'] })} options={KINDS.map(k => ({ value: k.id, label: k.label }))} placeholder="Not set" clearLabel="Not set" /></label>
        <label className="fact-row"><span className="muted small">Tags</span>
          <input value={tags} onChange={e => setTags(e.target.value)} onBlur={() => { if (tags !== m.tags) onSave({ tags }) }} placeholder="work, family" /></label>
        <p className="muted small">{m.engine && m.engine !== 'typed' ? `Written out by ${m.engine.replace(/^whisper /, 'whisper.cpp (').replace(/^api /, 'the API (')}${m.engine.startsWith('whisper') || m.engine.startsWith('api') ? ')' : ''}` : m.engine === 'typed' ? 'Typed' : ''}
          {m.ai_provider && `${m.engine ? ' · ' : ''}tidied up by ${m.ai_provider === 'pocket' ? 'Pocket AI' : 'your OpenAI key'}`}
          {m.has_audio && m.audio_bytes > 0 && ` · ${(m.audio_bytes / 1048576).toFixed(1)} MB`}</p>
      </div>
    </section>
  )
}

function AgainModal({ memo, onClose, onRun }: { memo: Memo; onClose: () => void; onRun: (lang: string) => void }) {
  const [lang, setLang] = useState(memo.language || '')
  const opts = ['bs', 'hr', 'sr', 'en', 'de', 'sl', 'mk', 'fr', 'it', 'es', 'tr', 'ru'].map(c => ({ value: c, label: langName(c) }))
  return (
    <Modal title="Write it out again" onClose={onClose} size="sm">
      <div className="grid form-grid">
        <p className="muted small">The words are written out again from the recording, in the language you choose. Your corrections to the words are replaced; the tidy version follows.</p>
        <Select ariaLabel="Language" value={lang} onChange={setLang} options={opts} placeholder="Find out by itself" clearLabel="Find out by itself" />
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" onClick={() => onRun(lang)}><Icon name="refresh" size={15} />Write it out again</button></div>
      </div>
    </Modal>
  )
}
