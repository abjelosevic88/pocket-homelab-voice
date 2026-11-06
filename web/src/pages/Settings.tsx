import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, AppSlug, AppStyle, DictationPage, DictationStyle, DictationStyles, CalendarItem, ConnMode, MoneyAccount, MoneyCategory, NoteFolder, Picked, Settings as SettingsT, Speech, StorageInfo, StorageKind, TaskList, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime, type DateFormat, type TimeFormat } from '../lib/format'
import { Avatar, Confirm, CoreHint, Field, Loading, Modal, Segmented, SettingRow, Switch, Tabs, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { ShortcutsSheet } from '../components/ShortcutsSheet'
import { PocketModelSelect } from '../components/PocketModelSelect'
import { announceChange } from '../lib/data'
import { LANG_NAMES } from '../lib/voice'

type TabId = 'general' | 'speech' | 'dictation' | 'notifications' | 'apps' | 'ai' | 'profile' | 'people' | 'tokens' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string }[] = [
  { id: 'general', label: 'General', icon: 'home', sub: 'Playback, your currency, dates and the app title.' },
  { id: 'speech', label: 'Writing out', icon: 'mic', sub: 'How your recordings become words: on this server with whisper.cpp, the language you speak, names to expect, and how long recordings are kept.' },
  { id: 'dictation', label: 'Dictation', icon: 'dictation', sub: 'How dictation from Pocket Launcher on your Mac is polished: a style for each app you dictate into.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'A long memo written out, one that could not be, and the morning list of memos to file.' },
  { id: 'apps', label: 'Connected apps', icon: 'layers', sub: 'Where memos go: Pocket Notes, Pocket Tasks, Pocket Calendar and Pocket Money.' },
  { id: 'ai', label: 'AI', icon: 'sparkles', sub: 'Who tidies memos up and finds the tasks and dates in them: Pocket AI through Core, or your own OpenAI key.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'people', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box.' },
  { id: 'tokens', label: 'API & shortcuts', icon: 'key', sub: 'Personal tokens for Siri Shortcuts, Homepage widgets and scripts.' },
  { id: 'data', label: 'Backup & data', icon: 'database', sub: 'Bring recordings in, export your memos with or without the audio, download the database.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the server.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const tab: TabId = TABS.find(t => t.id === raw)?.id || 'general'
  const current = TABS.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])
  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back" title="Back (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={TABS.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {TABS.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title"><h2>{current.label}</h2><p>{current.sub}</p></div>
            {tab === 'general' && <General />}
            {tab === 'speech' && <SpeechTab />}
            {tab === 'dictation' && <DictationTab />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'apps' && <Integrations />}
            {tab === 'ai' && <AITab />}
            {tab === 'profile' && <Profile />}
            {tab === 'people' && <Users />}
            {tab === 'tokens' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico"><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (patch?: Partial<SettingsT>) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return { f, setF, save, busy, isAdmin: user?.role === 'admin' }
}

function useTester() {
  const [test, setTest] = useState<Record<string, string>>({})
  const run = async (which: string, url: string, key: string, extra: Record<string, string> = {}) => {
    setTest(t => ({ ...t, [which]: 'testing…' }))
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/integrations/test`, { which, url, key, ...extra }); setTest(t => ({ ...t, [which]: r.ok ? `✓ ${r.message}` : `✕ ${r.error}` })) } catch (e) { setTest(t => ({ ...t, [which]: `✕ ${(e as Error).message}` })) }
  }
  const status = (which: string) => <span className={`small ${test[which]?.startsWith('✓') ? 'ok' : test[which]?.startsWith('✕') ? 'bad' : 'muted'}`}>{test[which]}</span>
  return { run, status }
}

const HOURS = Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: `${String(h).padStart(2, '0')}:00` }))

function General() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Playing memos" icon="play">
        <SettingRow title="Speed to start at" help="Change it any time with the speed button under the waveform."><Segmented size="sm" value={f.playback_speed} onChange={v => setF({ ...f, playback_speed: v })} options={['1', '1.25', '1.5', '2'].map(v => ({ value: v, label: `${v}×` }))} /></SettingRow>
      </Section>
      <Section title="Money" icon="wallet">
        <SettingRow title="Your currency" help="For an expense you mention without saying the currency."><input value={f.currency} onChange={e => setF({ ...f, currency: e.target.value.toUpperCase() })} maxLength={3} className="set-time" /></SettingRow>
      </Section>
      <Section title="Dates" icon="calendar">
        <SettingRow title="Week starts on"><Segmented size="sm" value={f.week_start} onChange={v => setF({ ...f, week_start: v })} options={[{ value: 1, label: 'Monday' }, { value: 6, label: 'Saturday' }, { value: 7, label: 'Sunday' }]} /></SettingRow>
        <SettingRow title="Date format"><Select ariaLabel="Date format" value={f.date_format} onChange={v => setF({ ...f, date_format: v as DateFormat })} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        <SettingRow title="Time format"><Select ariaLabel="Time format" value={f.time_format} onChange={v => setF({ ...f, time_format: v as TimeFormat })} options={TIME_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        {isAdmin && <SettingRow title="App title" help="Shown in the sidebar for everyone on this box."><input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} /></SettingRow>}
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

const KEEP = [{ value: '0', label: 'Forever' }, { value: '365', label: 'A year' }, { value: '180', label: 'Six months' }, { value: '90', label: 'Three months' }, { value: '30', label: 'A month' }]

function SpeechTab() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  const { toast } = useToast()
  const { data: sp, reload } = useAsync(() => api.get<Speech>(`${V1}/speech`), [])
  const downloading = !!sp?.models.some(m => m.download && !m.download.done)
  useEffect(() => {
    if (!downloading) return
    const t = window.setInterval(reload, 2000)
    return () => window.clearInterval(t)
  }, [downloading]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!f || !sp) return <Loading />
  const fetchModel = async (id: string) => { try { await api.post(`${V1}/speech/models/${id}`); reload() } catch (e) { toast((e as Error).message, 'err') } }
  const dropModel = async (id: string) => { try { await api.del(`${V1}/speech/models/${id}`); reload() } catch (e) { toast((e as Error).message, 'err') } }
  const langs = Object.entries(LANG_NAMES).map(([code, name]) => ({ value: code, label: name }))
  return (
    <div className="stack loose">
      <Section title="Where it is written out" icon="server" actions={<span className={`badge ${sp.ready ? 'ok' : 'warn'}`}>{sp.ready ? 'ready' : 'not ready'}</span>}
        sub="On this server, nothing leaves the box: whisper.cpp runs here. Or through an API: OpenAI with your own key, or a speech server on your network (speaches, LocalAI) at the base URL under AI.">
        <SettingRow title="Write out"><Segmented size="sm" value={f.engine} onChange={v => save({ engine: v })} options={[{ value: 'local', label: 'On this server' }, { value: 'api', label: 'OpenAI or a speech server' }]} /></SettingRow>
        {sp.detail && (f.engine === 'api' || sp.whisper) && <p className={`small ${sp.ready ? 'muted' : 'warn-text'}`}>{sp.detail[0].toUpperCase() + sp.detail.slice(1)}.</p>}
        {f.engine === 'api' && <SettingRow title="Model" help="Default gpt-4o-mini-transcribe. whisper-1 also gives timings, so the words follow the playback."><input value={f.speech_api_model} onChange={e => setF({ ...f, speech_api_model: e.target.value })} onBlur={() => save({ speech_api_model: f.speech_api_model })} placeholder={sp.api_model} /></SettingRow>}
      </Section>
      {f.engine === 'local' && <Section title="Speech models" icon="gauge" sub={<>The models this box writes with, for everyone: one for memos, one for dictation in other apps (Pocket Day, Pocket AI, the Mac launcher), where speed matters most. Dictation runs on {sp.threads} {sp.threads === 1 ? 'core' : 'cores'}; memos and meetings on {sp.back_threads}, at the lowest priority, so the box stays free.{!isAdmin && ' Admins choose them.'}</>}>
        {!sp.whisper && <div className="callout warn"><Icon name="alert" size={15} /><span>whisper.cpp is not installed here. The Docker image has it for Intel/AMD and ARM64 machines; elsewhere choose “OpenAI or a speech server” above.</span></div>}
        <SettingRow title="Memos are written out with" help="A new choice is downloaded right away; memos wait for it.">
          <Select ariaLabel="Model for memos" value={sp.model} disabled={!isAdmin} onChange={v => save({ speech_model: v }).then(reload)}
            options={sp.models.map(m => ({ value: m.id, label: `${m.name} · ${m.engine === 'parakeet' ? 'Parakeet' : 'Whisper'}`, disabled: !m.runs }))} />
        </SettingRow>
        <SettingRow title="Dictation is written out with" help="What other apps send to be written out (nothing is kept).">
          <Select ariaLabel="Model for dictation" value={sp.dictation_model} disabled={!isAdmin} onChange={v => save({ dictation_model: v }).then(reload)}
            options={sp.models.map(m => ({ value: m.id, label: `${m.name} · ${m.engine === 'parakeet' ? 'Parakeet' : 'Whisper'}`, disabled: !m.runs }))} />
        </SettingRow>
        <div className="model-list">{sp.models.map(m => (
          <div key={m.id} className={`model-row ${m.current || m.dictation ? 'cur' : ''}`}>
            <span className="grow model-info"><b>{m.name}</b> <span className="muted small">{m.engine === 'parakeet' ? 'Parakeet' : 'Whisper'} · about {Math.round(m.bytes / 1048576)} MB</span>
              {m.current && <span className="badge accent">memos</span>}{m.dictation && <span className="badge accent">dictation</span>}
              <span className="muted small block-line">{m.note}</span></span>
            <span className="model-state small">
              {!m.runs ? <span className="muted">not on this server</span>
                : m.download && !m.download.done ? <span className="tnum">{m.download.total ? Math.round(m.download.got * 100 / m.download.total) : 0}%</span>
                : m.ready ? <span className="ok">downloaded</span> : m.download?.error ? <span className="bad" title={m.download.error}>failed</span> : <span className="muted">not downloaded</span>}
              {isAdmin && m.runs && !m.ready && !(m.download && !m.download.done) && <button type="button" className="btn sm ghost" disabled={sp.demo} onClick={() => fetchModel(m.id)}><Icon name="download" size={14} />Get</button>}
              {isAdmin && m.ready && !m.current && !m.dictation && <button type="button" className="btn sm ghost danger-text" onClick={() => dropModel(m.id)}>Remove</button>}
            </span>
          </div>
        ))}</div>
        {sp.demo && <p className="muted small">This box shows demo data: models are not downloaded.</p>}
      </Section>}
      <Section title="What you say" icon="mic">
        <SettingRow title="Language" help="Automatic works for most memos; choose yours when short memos come out in the wrong language.">
          <Select ariaLabel="Language" value={f.language} onChange={v => save({ language: v })} options={langs} placeholder="Find out by itself" clearLabel="Find out by itself" />
        </SettingRow>
        <SettingRow title="Serbian in Latin letters" help="Whisper writes Serbian in Cyrillic; this turns it into Latin letters (also for Bosnian and Croatian, when it slips)."><Switch checked={f.latin} onChange={v => save({ latin: v })} label="Latin letters" /></SettingRow>
        <SettingRow title="Names and words to expect" stack help={<>People, places, products and words you use, separated by commas. They help Whisper spell them right (Parakeet doesn't use them). The <Link to="/dictionary">Dictionary</Link> is the better place for them: there you can also say what to replace, and it works for dictation too.</>}>
          <textarea rows={3} value={f.vocabulary} onChange={e => setF({ ...f, vocabulary: e.target.value })} onBlur={() => save({ vocabulary: f.vocabulary })} placeholder="Amra, Mirza, Kibe Mahala, Jahorina, Pocket Core" />
        </SettingRow>
      </Section>
      <Section title="Recordings" icon="archive">
        <SettingRow title="Keep recordings" help="After this the recording is let go and the words stay. Starred memos keep theirs.">
          <Select ariaLabel="Keep recordings" value={String(f.keep_audio_days)} onChange={v => save({ keep_audio_days: Number(v) })} options={KEEP} />
        </SettingRow>
      </Section>
      <StorageSection />
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

// StorageSection: where recordings are kept once written out — this server, a
// server folder the admin offers (a NAS), Pocket Files, Nextcloud or WebDAV.
// Choosing moves every recording there; new ones follow when written out.
function StorageSection() {
  const { toast } = useToast()
  const { data: info, reload } = useAsync(() => api.get<StorageInfo>(`${V1}/storage`), [])
  const [d, setD] = useState<{ kind: StorageKind; root: string; sub: string; path: string; url: string; user: string; password: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const running = !!info?.move.running
  useEffect(() => {
    if (!running) return
    const t = window.setInterval(reload, 1500)
    return () => window.clearInterval(t)
  }, [running]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!info || d) return
    const roots = info.options.find(o => o.kind === 'folder')?.roots || []
    const root = roots.find(r => info.kind === 'folder' && (info.path === r || info.path.startsWith(r + '/'))) || roots[0] || ''
    const sub = info.kind === 'folder' && root ? info.path.slice(root.length).replace(/^\/+/, '') : info.suggested_folder
    setD({ kind: info.kind, root, sub, path: info.kind === 'folder' ? '' : info.path || 'Voice', url: info.url, user: info.user, password: info.password })
  }, [info]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!info || !d) return null
  const opt = info.options.find(o => o.kind === d.kind)
  const set = (p: Partial<typeof d>) => setD({ ...d, ...p })
  const use = async () => {
    setBusy(true)
    const path = d.kind === 'folder' ? [d.root, d.sub.trim()].filter(Boolean).join('/') : d.path
    try {
      await api.put(`${V1}/storage`, { kind: d.kind, path, url: d.url, user: d.user, password: d.password })
      toast(d.kind === 'box' ? 'Recordings come back to this server' : 'Recordings are moving there')
      reload()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const { kept_there: there, waiting_here: here, elsewhere, move } = info
  return (
    <Section title="Where recordings are kept" icon="server" actions={<span className="badge">{info.where}</span>}
      sub="Recordings arrive here and are written out here; then they move to the place you choose and play from there. Choosing another place moves every recording over, and this server brings them back.">
      <p className="small muted">
        {there.count} {there.count === 1 ? 'recording' : 'recordings'} ({fmtBytes(there.bytes)}) in {info.where}
        {here.count > 0 && <> · {here.count} on this server, moving when written out</>}
        {elsewhere.count > 0 && <> · {elsewhere.count} still at the place before</>}
      </p>
      {move.running && <p className="small"><Icon name="refresh" size={13} /> Moving {move.done} of {move.total}…</p>}
      {!move.running && move.error && <p className="small warn-text">Last move: {move.error}{move.failed > 0 && ` (${move.failed} not moved; tried again within the hour)`}</p>}
      <SettingRow title="Keep them on">
        <Select ariaLabel="Where recordings are kept" value={d.kind} onChange={v => set({ kind: v as StorageKind })}
          options={info.options.map(o => ({ value: o.kind, label: o.name, disabled: !o.ready }))} />
      </SettingRow>
      {opt && !opt.ready && opt.why && <p className="small muted">{opt.why[0].toUpperCase() + opt.why.slice(1)}.</p>}
      {d.kind === 'folder' && opt?.ready && <>
        <SettingRow title="Folder" help="Folders the admin made available on this server, such as a NAS share.">
          <Select ariaLabel="Server folder" value={d.root} onChange={v => set({ root: v })} options={(opt.roots || []).map(r => ({ value: r, label: r }))} />
        </SettingRow>
        <SettingRow title="Your folder in it" help="Each recording goes in a year/month folder, named by its day, time and title.">
          <input value={d.sub} onChange={e => set({ sub: e.target.value })} placeholder={info.suggested_folder} />
        </SettingRow>
      </>}
      {d.kind === 'files' && <SettingRow title="Folder in your files" help="Reached through Pocket Core: allow Voice → Files in Pocket Core → Links once.">
        <input value={d.path} onChange={e => set({ path: e.target.value })} placeholder="Voice" />
      </SettingRow>}
      {(d.kind === 'nextcloud' || d.kind === 'webdav') && <>
        <SettingRow title={d.kind === 'nextcloud' ? 'Nextcloud address' : 'WebDAV address'}>
          <input value={d.url} onChange={e => set({ url: e.target.value })} placeholder={d.kind === 'nextcloud' ? 'https://cloud.example.com' : 'https://nas.local/webdav'} />
        </SettingRow>
        <SettingRow title="Username"><input value={d.user} onChange={e => set({ user: e.target.value })} autoComplete="off" /></SettingRow>
        <SettingRow title={d.kind === 'nextcloud' ? 'App password' : 'Password'} help={d.kind === 'nextcloud' ? 'Nextcloud → Settings → Security → Devices & sessions → Create new app password.' : undefined}>
          <input type="password" value={d.password} onChange={e => set({ password: e.target.value })} autoComplete="new-password" />
        </SettingRow>
        <SettingRow title="Folder"><input value={d.path} onChange={e => set({ path: e.target.value })} placeholder="Voice" /></SettingRow>
      </>}
      <div className="row"><button type="button" className="btn primary" onClick={use} disabled={busy || running || (opt && !opt.ready)}>{d.kind === 'box' ? 'Keep them on this server' : 'Check and move them there'}</button></div>
    </Section>
  )
}

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { toast } = useToast()
  const { run, status } = useTester()
  if (!f) return <Loading />
  const now = async () => { try { const r = await api.post<{ sent: boolean; body: string }>(`${V1}/notify/check`); toast(r.sent ? 'Sent' : r.body ? `Nothing sent (no push URL): ${r.body.split('\n')[0]}` : 'Nothing waits longer than a day') } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <div className="stack loose">
      <Section title="Push" icon="bell" actions={<span className={`badge ${f.notify_configured ? 'ok' : ''}`}>{f.notify_configured ? 'on' : 'not set'}</span>}
        sub={<>Where messages go: an Apprise gateway (<code>…/notify/&lt;key&gt;</code>), an ntfy topic URL, or any webhook that takes JSON.</>}>
        <SettingRow title="URL" stack><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="http://apprise:8000/notify/voice" /></SettingRow>
        <SettingRow title="Format"><Segmented value={f.notify_format} onChange={v => setF({ ...f, notify_format: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'json', label: 'JSON' }]} size="sm" /></SettingRow>
      </Section>
      <Section title="What to tell you" icon="clock">
        <SettingRow title="Written out" help="When a memo of three minutes or more, or a recording you added, is written out."><Switch checked={f.notify_done} onChange={v => setF({ ...f, notify_done: v })} label="Written out" /></SettingRow>
        <SettingRow title="Could not be written out" help="With why, so nothing is lost quietly."><Switch checked={f.notify_failed} onChange={v => setF({ ...f, notify_failed: v })} label="Failed" /></SettingRow>
        <SettingRow title="Memos to file" help="One message in the morning when memos older than a day wait in the inbox."><Switch checked={f.notify_inbox} onChange={v => setF({ ...f, notify_inbox: v })} label="Memos to file" /></SettingRow>
        <SettingRow title="Morning message at" help="Server time."><Select ariaLabel="Hour" value={String(f.notify_hour)} onChange={v => setF({ ...f, notify_hour: Number(v) })} options={HOURS} /></SettingRow>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        <button type="button" className="btn" onClick={() => run('notify', f.notify_url, '', { format: f.notify_format })} disabled={!f.notify_url}><Icon name="mail" size={15} />Send a test</button>
        <button type="button" className="btn" onClick={now}><Icon name="refresh" size={15} />Check now</button>
        {status('notify')}
      </div>
    </div>
  )
}

const APPS: { slug: AppSlug; name: string; icon: string; title: string; sub: string; token: string; port: string }[] = [
  { slug: 'notes', name: 'Pocket Notes', icon: 'note', title: 'Pocket Notes — notes and today’s note', sub: 'Save a memo as a note in the folder you choose (the summary on top, a link back to the recording at the bottom), or add a line to today’s daily note.', token: 'Pocket Notes → Settings → API tokens', port: '8177' },
  { slug: 'tasks', name: 'Pocket Tasks', icon: 'check-square', title: 'Pocket Tasks — what you have to do', sub: 'The tasks found in a memo, with the day you said, go to your inbox or a list you choose, each with a link back to the memo.', token: 'Pocket Tasks → Settings → API tokens', port: '8185' },
  { slug: 'calendar', name: 'Pocket Calendar', icon: 'calendar', title: 'Pocket Calendar — appointments', sub: 'An appointment you mentioned (“dentist on Tuesday at nine”) goes on your calendar with where it is.', token: 'Pocket Calendar → Settings → API tokens', port: '8190' },
  { slug: 'money', name: 'Pocket Money', icon: 'wallet', title: 'Pocket Money — money you spent', sub: '“Twelve euros for lunch” becomes an expense when you press Book. Nothing is booked by itself.', token: 'Pocket Money → Settings → API tokens', port: '8178' },
]

function Integrations() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const { toast } = useToast()
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const eff = (m: ConnMode): ConnMode => !coreOn && m === 'core' ? 'off' : m
  const set = (k: string, v: string) => setF({ ...f, [k]: v } as SettingsT)
  return (
    <div className="stack loose">
      <div className="callout info"><Icon name="info" size={15} /><div>These connections are yours. Through <b>Pocket Core</b> Voice acts as you in your own Pocket apps with nothing to paste (switch it off for yourself in Pocket Core → Settings → Apps acting for me); <b>Direct</b> uses a URL and an API token you enter.{!coreOn && <CoreHint />}</div></div>
      {APPS.map(a => {
        const mode = f[`${a.slug}_mode`]
        const via = f[`${a.slug}_via`]
        return (
          <Section key={a.slug} title={a.title} icon={a.icon} actions={connBadge(eff(mode), via)} sub={a.sub}>
            <Connection app={a.name} mode={mode} via={via} detail={f[`${a.slug}_detail`]} core={coreOn} hasToken={!!f[`${a.slug}_token`]}
              onMode={m => save({ [`${a.slug}_mode`]: m } as Partial<SettingsT>)} onDisconnect={() => save({ [`${a.slug}_url`]: '', [`${a.slug}_token`]: '' } as Partial<SettingsT>)}
              test={<><button type="button" className="btn sm" onClick={() => run(a.slug, f[`${a.slug}_url`], f[`${a.slug}_token`])}>Test connection</button>{status(a.slug)}</>}
              direct={<div className="two-col tight">
                <Field label="URL" help="Inside Docker use http://host.docker.internal:PORT or the service name."><input value={f[`${a.slug}_url`]} onChange={e => set(`${a.slug}_url`, e.target.value)} placeholder={`http://host.docker.internal:${a.port}`} /></Field>
                <Field label="API token" help={a.token}><input value={f[`${a.slug}_token`]} onChange={e => set(`${a.slug}_token`, e.target.value)} autoComplete="off" /></Field>
              </div>} />
            {a.slug === 'notes' && eff(mode) !== 'off' && via && <NotesOptions f={f} save={save} />}
            {a.slug === 'tasks' && eff(mode) !== 'off' && via && <TasksOptions f={f} save={save} />}
            {a.slug === 'calendar' && eff(mode) !== 'off' && via && <CalendarOptions f={f} save={save} />}
            {a.slug === 'money' && eff(mode) !== 'off' && via && <MoneyOptions f={f} setF={setF} save={save} />}
          </Section>
        )
      })}
      <Section title="After sending" icon="inbox">
        <SettingRow title="Sending takes a memo out of the inbox" help="Off: a memo stays in the inbox until you tick it, however many things you sent from it."><Switch checked={f.file_on_send} onChange={v => save({ file_on_send: v })} label="Out of the inbox" /></SettingRow>
      </Section>
      <Section title="Other Pocket apps" icon="layers" sub="Voice offers your memos to the rest of the box through Pocket Core: Pocket AI finds “what did I say about the garden?” and can keep a memo for you, Pocket Day writes the day’s memos into your journal, and other apps can have their dictation written out here instead of by a cloud service. Switched off until you allow them in Pocket Core → Links.">
        <dl className="set-dl">
          <div><dt>voice:read</dt><dd className="muted">Your memos: their words, the tidy version and summary, what they are about, where you sent them and the recording.</dd></div>
          <div><dt>voice:write</dt><dd className="muted">Add a recording or a dictated text as a memo; that app may change or remove only the memos it added.</dd></div>
          <div><dt>voice:transcribe</dt><dd className="muted">Send a short recording and get the words back, written out on this server with your language and words. Nothing is kept.</dd></div>
        </dl>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function MoneyOptions({ f, setF, save }: { f: SettingsT; setF: (s: SettingsT) => void; save: (p?: Partial<SettingsT>) => void }) {
  const { data: accts } = useAsync(() => api.get<Picked<MoneyAccount>>(`${V1}/links/money/accounts`), [])
  const { data: cats } = useAsync(() => api.get<Picked<MoneyCategory>>(`${V1}/links/money/categories?account_id=${f.money_account_id || 0}`), [f.money_account_id])
  return (
    <div className="mt">
      <SettingRow title="Account" help="Where an expense is booked; otherwise your everyday account.">
        <Select ariaLabel="Account" value={f.money_account_id ? String(f.money_account_id) : ''} onChange={v => { setF({ ...f, money_account_id: Number(v) || 0 }); save({ money_account_id: Number(v) || 0 }) }} clearLabel="Everyday account" placeholder="Everyday account"
          options={(accts?.items || []).map(a => ({ value: String(a.id), label: a.name, hint: a.currency }))} />
      </SettingRow>
      <SettingRow title="Category" help="Otherwise Pocket Money picks one.">
        <Select ariaLabel="Category" value={f.money_category_id ? String(f.money_category_id) : ''} onChange={v => { setF({ ...f, money_category_id: Number(v) || 0 }); save({ money_category_id: Number(v) || 0 }) }} clearLabel="Let Money choose" placeholder="Let Money choose"
          options={(cats?.items || []).map(c => ({ value: String(c.id), label: c.full_name || c.name }))} />
      </SettingRow>
      {accts && !accts.connected && <p className="bad small">{accts.detail}</p>}
    </div>
  )
}

function CalendarOptions({ f, save }: { f: SettingsT; save: (p?: Partial<SettingsT>) => void }) {
  const { data: cals } = useAsync(() => api.get<Picked<CalendarItem>>(`${V1}/links/calendar/calendars`), [])
  return (
    <div className="mt">
      <SettingRow title="Calendar" help="Otherwise your default calendar.">
        <Select ariaLabel="Calendar" value={f.calendar_id ? String(f.calendar_id) : ''} onChange={v => save({ calendar_id: Number(v) || 0 })} clearLabel="My default calendar" placeholder="My default calendar"
          options={(cals?.items || []).map(c => ({ value: String(c.id), label: c.name }))} />
      </SettingRow>
    </div>
  )
}

function NotesOptions({ f, save }: { f: SettingsT; save: (p?: Partial<SettingsT>) => void }) {
  const { data } = useAsync(() => api.get<Picked<NoteFolder>>(`${V1}/links/notes/folders`), [])
  const opts = (data?.items || []).map(x => ({ value: x.path, label: x.path }))
  if (f.notes_folder && !opts.some(o => o.value === f.notes_folder)) opts.unshift({ value: f.notes_folder, label: `${f.notes_folder} (made with the first note)` })
  return (
    <div className="mt">
      <SettingRow title="Folder" help="Where a memo saved as a note goes; you can pick another each time.">
        <Select ariaLabel="Folder" value={f.notes_folder} onChange={v => save({ notes_folder: v })} options={opts} placeholder="The top of your notes" clearLabel="The top of your notes" onCreate={l => l.trim()} createLabel="New folder" />
      </SettingRow>
      <SettingRow title="A link to the recording" help="“Listen in Pocket Voice” under the note."><Switch checked={f.notes_listen} onChange={v => save({ notes_listen: v })} label="Listen link" /></SettingRow>
      {data && !data.connected && <p className="bad small">{data.detail}</p>}
    </div>
  )
}

function TasksOptions({ f, save }: { f: SettingsT; save: (p?: Partial<SettingsT>) => void }) {
  const { data } = useAsync(() => api.get<Picked<TaskList>>(`${V1}/links/tasks/lists`), [])
  return (
    <div className="mt">
      <SettingRow title="List" help="Where tasks go; otherwise your inbox in Pocket Tasks.">
        <Select ariaLabel="List" value={f.tasks_list_id ? String(f.tasks_list_id) : ''} onChange={v => save({ tasks_list_id: Number(v) || 0 })} options={(data?.items || []).map(l => ({ value: String(l.id), label: l.name }))} placeholder="Your inbox" clearLabel="Your inbox" />
      </SettingRow>
      {data && !data.connected && <p className="bad small">{data.detail}</p>}
    </div>
  )
}

function AITab() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const { data: ai } = useAsync(() => api.get<{ ready: boolean; provider: string; detail: string }>(`${V1}/ai/status`), [])
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const shown = !coreOn && f.ai_provider === 'pocket' ? 'openai' : (f.ai_provider || 'auto')
  return (
    <div className="stack loose">
      <Section title="What the AI does" icon="sparkles" sub="After a memo is written out, its words (never the recording) go to the AI, which tidies them up, gives a title and a short summary, says what the memo is about, and finds the tasks, appointments, journal lines and money in it. Nothing goes to another app until you press its button. Questions about your memos go to Pocket AI through the Ask button at the top.">
        <SettingRow title="Tidy up every memo" help="Off: only when you press “Tidy up with AI” on a memo."><Switch checked={f.ai_tidy} onChange={v => save({ ai_tidy: v })} label="Tidy up" /></SettingRow>
        {ai && (ai.ready ? <p className="muted small row" style={{ gap: 6 }}><Icon name="sparkles" size={13} />{ai.provider === 'pocket' ? 'Pocket AI' : 'Your OpenAI key'} is ready.</p>
          : <div className="callout info"><Icon name="info" size={15} /><div>No AI connected{ai.detail ? ` (${ai.detail})` : ''}.{!coreOn && <CoreHint />}</div></div>)}
      </Section>
      <Section title="Provider" icon="sparkles">
        <div className="grid" style={{ gap: 12 }}>
          <Field group label="Who writes" help="Automatic prefers your enabled Pocket AI connection; without one it uses your OpenAI settings.">
            <Select ariaLabel="AI provider" value={shown} onChange={v => setF({ ...f, ai_provider: v as SettingsT['ai_provider'] })} options={[{ value: 'auto', label: coreOn ? 'Automatic — prefer Pocket AI' : 'Automatic' }, { value: 'pocket', label: 'Pocket AI', disabled: !coreOn, hint: coreOn ? undefined : 'needs Pocket Core' }, { value: 'openai', label: 'OpenAI / compatible API' }]} />
          </Field>
          {!coreOn && <CoreHint />}
          {coreOn && shown !== 'openai' && <PocketModelSelect value={f.ai_pocket_model || ''} onChange={v => setF({ ...f, ai_pocket_model: v })} />}
          {(shown === 'openai' || (!coreOn && shown === 'auto')) && <>
            <Field label="OpenAI API key" help="platform.openai.com → API keys. Stored on the server; shown masked."><input value={f.openai_api_key} onChange={e => setF({ ...f, openai_api_key: e.target.value })} autoComplete="off" placeholder="sk-…" /></Field>
            <div className="two-col tight">
              <Field label="Model" help="Default gpt-4o-mini."><input value={f.openai_model} onChange={e => setF({ ...f, openai_model: e.target.value })} placeholder="gpt-4o-mini" /></Field>
              <Field label="Base URL (optional)" help="Ollama, LM Studio, OpenRouter… With “OpenAI or a speech server” under Writing out, recordings go here too."><input value={f.openai_base_url} onChange={e => setF({ ...f, openai_base_url: e.target.value })} placeholder="https://api.openai.com/v1" /></Field>
            </div>
          </>}
        </div>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        {(shown === 'openai' || (!coreOn && shown === 'auto')) && <><button type="button" className="btn" onClick={() => run('openai', f.openai_base_url, f.openai_api_key)} disabled={!f.openai_api_key}>Test the OpenAI key</button>{status('openai')}</>}
      </div>
    </div>
  )
}

function Connection({ app, mode: saved, via, detail, core, onMode, onDisconnect, hasToken, test, direct }: {
  app: string; mode: ConnMode; via: string; detail: string; core: boolean; onMode: (m: ConnMode) => void; onDisconnect: () => void
  hasToken: boolean; test: React.ReactNode; direct: React.ReactNode
}) {
  const [advanced, setAdvanced] = useState(saved === 'direct')
  const mode: ConnMode = !core && saved === 'core' ? 'off' : saved
  const ok = via !== '' && mode !== 'off'
  const showDirect = !core || mode === 'direct' || advanced
  const line = mode === 'off' ? `Off: Voice does not talk to ${app}.`
    : mode === 'core' ? (ok ? `Connected through Pocket Core, as you${detail ? ` (${detail})` : ''}. Nothing to fill in; switch it off for yourself in Pocket Core → Settings → Apps acting for me.` : `Not connected: ${detail}.`)
    : (ok ? `Direct connection with your own ${app} URL and token; use Test connection to check it.` : `Not connected: ${detail || 'add the URL and a token'}.`)
  return (
    <div className="conn-block">
      <div className="conn-mode">
        <Segmented size="sm" value={mode} onChange={m => onMode(m as ConnMode)} options={[
          { value: 'core', label: 'Pocket Core', title: core ? 'Through Pocket Core: per person, nothing to paste' : 'Needs Pocket Core: there is none on this box', disabled: !core },
          ...(showDirect ? [{ value: 'direct', label: 'Direct', title: 'Your own URL and API token' }] : []),
          { value: 'off', label: 'Off' },
        ]} />
        {!showDirect && <button type="button" className="conn-advanced" onClick={() => setAdvanced(true)}>Advanced: connect directly with a URL and key</button>}
      </div>
      <div className={`conn-status ${mode === 'off' ? '' : ok ? 'ok' : 'bad'}`}><span className="dot" />{line}</div>
      {mode === 'direct' && <>
        {direct}
        <div className="row wrap">{test}{hasToken && <button type="button" className="btn sm ghost danger-text" onClick={onDisconnect} title="Remove the saved URL and token">Disconnect</button>}</div>
      </>}
      {mode === 'core' && <div className="row wrap">{test}</div>}
    </div>
  )
}

const connBadge = (mode: ConnMode, via: string) => mode === 'off' ? <span className="badge">off</span>
  : via ? <span className="badge ok">{via === 'core' ? 'Pocket Core' : 'direct'}</span>
  : <span className="badge warn">not connected</span>

function Users() {
  const { user, users, reloadMeta, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); await reloadMeta(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Section title="People on this box" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={14} />Add person</button>}
      sub="Everyone signs in with their own account and has their own memos, settings and connections. Admins manage accounts; they do not see anyone else’s memos.">
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar name={u.name} email={u.email} size={34} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge accent">admin</span>}</div>
              <div className="muted small ellipsis">{u.email}</div>
            </div>
            {isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit; passwords are not used here.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadMeta() }} />}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name || del.email}?`} message="Their account is deleted together with their memos and recordings." onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); run(() => api.del(`${V1}/users/${d.id}`), 'User removed') }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f); toast(user.id ? 'User saved' : 'User added'); onSaved() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit person' : 'Add a person'} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add person'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={save} className="stack loose">
      <div className="set-profile">
        <Avatar name={user?.name} email={user?.email} size={56} />
        <div className="grow"><h3>{user?.name || user?.email}</h3><p className="muted small">{user?.email} · {user?.role}</p></div>
      </div>
      <Section title="You">
        <div className="stack">
          <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
          <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
        </div>
      </Section>
      {status?.auth_mode === 'local' && <Section title="Password" sub="Leave empty to keep the current one."><div className="stack">
        <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
        <Field label="New password" help="At least 8 characters."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
      </div></Section>}
      <div className="row"><button className="btn primary" disabled={busy}>Save changes</button></div>
    </form>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { settings } = useApp()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const base = settings?.base_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="Personal tokens" icon="key" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you: it can read and change everything, feed a Homepage widget (<code>GET /api/v1/summary</code>) or send a recording from a Siri Shortcut. See <code>docs/api.md</code>.</>}>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. iPhone Shortcut)" /><button className="btn primary"><Icon name="plus" size={15} />Create</button></form>
        {fresh && <div className="set-fresh mb"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><pre className="mono">{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="muted">{fmtDateTime(t.created_at)}</td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm confirmLabel="Revoke" title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Section>
      <Section title="Endpoints" icon="code" sub={<>At <code>{base}/api/v1</code>.</>}>
        <dl className="set-dl">
          <div><dt><code>POST /memos</code></dt><dd className="muted">Multipart field <code>file</code> (any recording) — a Siri Shortcut “Record audio → Get contents of URL” sends a memo from the lock screen. Or JSON <code>{'{"text":"…"}'}</code> for a typed one.</dd></div>
          <div><dt><code>POST /transcribe</code></dt><dd className="muted">Multipart <code>file</code> → <code>{'{"text","language","segments"}'}</code>: words back, nothing kept.</dd></div>
          <div><dt><code>GET /summary</code></dt><dd className="muted">How many memos wait in the inbox, today's and the latest — for Homepage.</dd></div>
          <div><dt><code>GET /day/YYYY-MM-DD</code></dt><dd className="muted">One day's memos — what Pocket Day reads.</dd></div>
          <div><dt><code>GET /search?q=</code></dt><dd className="muted">Find memos by their words.</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Data() {
  const { toast } = useToast()
  const { user } = useApp()
  const [busy, setBusy] = useState(false)
  const importFile = async (file: File) => {
    setBusy(true)
    try {
      const res = await fetch(`${V1}/import`, { method: 'POST', body: await file.text(), headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin' })
      const j = await res.json()
      if (!res.ok) throw new Error(j.error)
      toast(`Imported: ${j.memos} memos${j.sent ? `, ${j.sent} sent items` : ''}${j.dictations ? `, ${j.dictations} dictations` : ''}${j.dictionary ? `, ${j.dictionary} dictionary words` : ''}`)
      announceChange()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <div className="stack loose">
      <Section title="Recordings you already have" icon="upload" sub="Apple Voice Memos (select memos → share → Save to Files), your phone's recorder app, WhatsApp voice notes, or any audio file. Each becomes a memo with the day it was recorded and is written out here, one after another.">
        <button type="button" className="btn primary" onClick={() => window.dispatchEvent(new CustomEvent('pvo:create', { detail: { kind: 'upload' } }))}><Icon name="upload" size={15} />Add recordings…</button>
      </Section>
      <Section title="Export" icon="archive" sub="Everything of yours: the words and what the AI made of them as one JSON file, or every recording with its words in a ZIP, a folder per day. A Pocket Voice JSON export goes back in below (the words; the recordings come back through Add recordings).">
        <div className="row wrap"><a className="btn" href={`${V1}/export.json`}><Icon name="download" size={15} />Export JSON</a><a className="btn" href={`${V1}/export.zip`}><Icon name="download" size={15} />Recordings and words (ZIP)</a></div>
        <label className="btn mt"><Icon name="upload" size={15} />{busy ? 'Importing…' : 'Import a Pocket Voice export…'}<input type="file" accept=".json,application/json" hidden onChange={e => e.target.files?.[0] && importFile(e.target.files[0])} /></label>
      </Section>
      {user?.role === 'admin' && <Section title="Database backup" icon="database" sub="The whole box, every account — admins only.">
        <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>). The recordings are files next to it, in <code>audio/</code> of the data folder; back that folder up too. Restore by stopping the app and replacing <code>pocket-voice.db</code>.</p>
        <a className="btn" href={`${V1}/backup`}><Icon name="database" size={15} />Download database</a>
      </Section>}
    </div>
  )
}

function About() {
  const { data, error } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const [keys, setKeys] = useState(false)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const counts = (data.counts || {}) as Record<string, number>
  const core = (data.core || {}) as { configured?: boolean; registered?: boolean; url?: string; error?: string }
  const tiles: [string, number | undefined, string][] = [['Memos', counts.memos, 'mic'], ['Dictations', counts.dictations, 'dictation'], ['Sent to apps', counts.sent, 'send'], ['Tokens', counts.api_tokens, 'key']]
  const rows: [string, React.ReactNode][] = [
    ['Version', <code key="v">{String(data.version)}</code>],
    ['Uptime', String(data.uptime)],
    ['Runtime', `${String(data.go)} · ${String(data.os)}/${String(data.arch)}`],
    ['Time zone', String(data.timezone)],
    ['Sign-in', String(data.auth_mode)],
    ['Pocket Core', core.configured ? (core.registered ? <span key="c" className="ok">connected · {core.url}</span> : <span key="c" className="bad">{core.error || 'not registered yet'}</span>) : <span key="c" className="muted">not configured</span>],
    ['Database', <span key="db"><code>{String(data.db_path)}</code> · {fmtBytes(Number(data.db_bytes))}</span>],
    ['People', `${counts.users ?? 0} accounts on this box`],
  ]
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Voice</h3>
          <p className="muted small">Voice memos you record on your phone, written out on your own server with whisper.cpp, tidied up by Pocket AI, and filed where they belong: notes, today's note, tasks, your calendar, Pocket Money. One binary, one SQLite file, one volume.</p>
        </div>
      </div>
      <div className="set-stats">
        {tiles.map(([label, n, icon]) => (
          <div key={label} className="set-stat"><Icon name={icon} size={16} /><span className="set-stat-n tnum">{(n ?? 0).toLocaleString()}</span><span className="set-stat-l">{label}</span></div>
        ))}
      </div>
      <Section title="This server">
        <dl className="set-dl">
          {rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}
        </dl>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn" onClick={() => setKeys(true)}><Icon name="keyboard" size={15} />Keyboard shortcuts</button>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-voice" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
      </div>
      <p className="muted small">Open source (MIT). Part of the Pocket apps: Pocket Core connects them — Notes, Tasks, Calendar, Money, Day, Pocket AI and more. Speech recognition by whisper.cpp (MIT) with OpenAI's Whisper models.</p>
      {keys && <ShortcutsSheet onClose={() => setKeys(false)} />}
    </div>
  )
}

const STYLES: { id: DictationStyle; label: string; help: string }[] = [
  { id: 'natural', label: 'Natural', help: 'Punctuation and capitals, filler words and false starts out, your own words kept.' },
  { id: 'casual', label: 'Casual', help: 'Relaxed, for chat: lower case is fine, no greetings or sign-offs added.' },
  { id: 'formal', label: 'Formal', help: 'Complete sentences, proper capitals and punctuation, paragraphs: for email and documents.' },
  { id: 'code', label: 'Code', help: 'Names, paths and commands kept exactly, code in backticks, no full stop at the end: for editors and terminals.' },
  { id: 'none', label: 'No AI', help: 'Typed as heard, only your dictionary’s replacements.' },
]
const styleOptions = STYLES.map(s => ({ value: s.id, label: s.label, hint: s.help }))

function DictationTab() {
  const { toast } = useToast()
  const [st, setSt] = useState<DictationStyles | null>(null)
  const [seen, setSeen] = useState<{ app: string; app_id: string }[]>([])
  const [name, setName] = useState('')
  const [bundle, setBundle] = useState('')
  useEffect(() => {
    api.get<DictationStyles>(`${V1}/dictation/styles`).then(setSt).catch(e => toast((e as Error).message, 'err'))
    // the apps you dictated into, for quick picks
    api.get<DictationPage>(`${V1}/dictations?limit=200`).then(r => {
      const m = new Map<string, { app: string; app_id: string }>()
      for (const d of r.items) if (d.app || d.app_id) m.set((d.app_id || d.app).toLowerCase(), { app: d.app, app_id: d.app_id })
      setSeen([...m.values()])
    }).catch(() => {})
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const put = async (next: DictationStyles) => {
    const prev = st
    setSt(next)
    try { setSt(await api.put<DictationStyles>(`${V1}/dictation/styles`, next)); toast('Saved') } catch (e) { setSt(prev); toast((e as Error).message, 'err') }
  }
  if (!st) return <Loading />
  const key = (a: { app: string; app_id: string }) => (a.app_id || 'name:' + a.app).toLowerCase()
  const have = new Set(st.apps.map(key))
  const addApp = (a: { app: string; app_id: string }, style: DictationStyle = 'natural') => {
    if (!a.app.trim() && !a.app_id.trim()) return
    if (have.has(key(a))) { toast('That app has a style already', 'err'); return }
    put({ ...st, apps: [...st.apps, { app: a.app.trim(), app_id: a.app_id.trim(), style }] })
    setName(''); setBundle('')
  }
  const picks = seen.filter(a => !have.has(key(a)))
  return (
    <div className="stack loose">
      <Section title="Styles" icon="sparkles" sub="When you dictate on the Mac, your AI tidies the words before they are typed. The style decides how much, and can be different for each app.">
        <div className="style-legend">{STYLES.map(s => <div key={s.id}><b>{s.label}</b><span className="muted small">{s.help}</span></div>)}</div>
        <SettingRow title="Everywhere else" help="The style for apps without one of their own.">
          <Select ariaLabel="Default style" value={st.default} onChange={v => put({ ...st, default: v as DictationStyle })} options={styleOptions} />
        </SettingRow>
      </Section>
      <Section title="Per app" icon="layers" sub="An app is matched by its bundle id (com.tinyspeck.slackmacgap) or, without one, by its name.">
        {st.apps.length === 0 && <p className="muted small">No app has its own style yet.</p>}
        {st.apps.map((a: AppStyle, i: number) => (
          <SettingRow key={key(a) + i} title={a.app || a.app_id} help={a.app && a.app_id ? <code>{a.app_id}</code> : a.app_id ? undefined : 'matched by name'}>
            <div className="row">
              <Select ariaLabel={`Style for ${a.app || a.app_id}`} value={a.style} onChange={v => put({ ...st, apps: st.apps.map((x, j) => (j === i ? { ...x, style: v as DictationStyle } : x)) })} options={styleOptions} />
              <button type="button" className="btn ghost icon-only sm" aria-label={`Remove ${a.app || a.app_id}`} title="Remove" onClick={() => put({ ...st, apps: st.apps.filter((_, j) => j !== i) })}><Icon name="trash" size={15} /></button>
            </div>
          </SettingRow>
        ))}
        <form className="style-add" onSubmit={e => { e.preventDefault(); addApp({ app: name, app_id: bundle }) }}>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="App name (Slack)" aria-label="App name" maxLength={200} />
          <input value={bundle} onChange={e => setBundle(e.target.value)} placeholder="Bundle id (optional)" aria-label="Bundle id" maxLength={255} />
          <button type="submit" className="btn" disabled={!name.trim() && !bundle.trim()}><Icon name="plus" size={15} />Add app</button>
        </form>
        {picks.length > 0 && <div className="stack tight">
          <span className="muted small">Apps you dictated into:</span>
          <div className="chips">{picks.slice(0, 16).map(a => <button key={key(a)} type="button" className="chip" title={a.app_id || undefined} onClick={() => addApp(a)}><Icon name="plus" size={13} />{a.app || a.app_id}</button>)}</div>
        </div>}
      </Section>
      <p className="muted small">Your <Link to="/dictionary">Dictionary</Link> works in every style: its words are spelled your way and its replacements are always made.</p>
    </div>
  )
}
