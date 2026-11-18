// Left column: record (and the add menu), the inbox with how many wait,
// all and starred memos, your folders, what they are about, your tags — the Pocket
// Calendar sidebar.
import { useState } from 'react'
import { Link, NavLink, useNavigate } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { useData } from '../lib/data'
import { KINDS } from '../lib/voice'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { DeleteFolder, FolderNameModal } from './Folders'
import type { Folder } from '../lib/types'
import { type Anchor, ContextMenu, Menu, rectOf, useContextMenu } from './ui'

export function Sidebar({ onCreate, onNavigate }: { onCreate: (k: CreateKind) => void; onNavigate: () => void }) {
  const { status, settings } = useApp()
  const { counts, kinds, tags, folders } = useData()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const [making, setMaking] = useState(false)
  const navigate = useNavigate()
  const title = settings?.title || status?.title || 'Pocket Voice'
  const nav: { to: string; label: string; icon: string; end?: boolean; count?: number; hint?: string }[] = [
    { to: '/', label: 'Inbox', icon: 'inbox', end: true, count: counts?.inbox, hint: 'Memos to deal with' },
    { to: '/memos', label: 'All memos', icon: 'list', count: counts?.all },
    { to: '/starred', label: 'Starred', icon: 'star', count: counts?.starred },
    { to: '/dictation', label: 'Dictation', icon: 'dictation' },
    { to: '/dictionary', label: 'Dictionary', icon: 'dictionary' },
    { to: '/people', label: 'People', icon: 'users', hint: 'Voices you named in meetings' },
  ]
  return (
    <aside className="sidebar" aria-label="Pocket Voice">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Voice' ? title : <>Pocket <b>Voice</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <div className="create-split">
        <button type="button" className="btn primary create-btn" onClick={() => onCreate('record')} title="Record a memo (R)">
          <Icon name="mic" size={15} strokeWidth={2.2} />Record
        </button>
        <button type="button" className="btn primary create-more" aria-label="More ways to add" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
          <Icon name="chevron-down" size={15} />
        </button>
      </div>
      {menu && <Menu anchor={menu} items={createItems(onCreate)} onClose={() => setMenu(null)} width={230} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {!!n.count && <span className="sb-count" title={n.hint}>{n.count}</span>}
          </NavLink>
        ))}
      </nav>
      <p className={`sb-working muted small ${counts && counts.working > 0 ? '' : 'idle'}`} aria-live="polite"><Icon name="waves" size={13} />{counts && counts.working > 1 ? `${counts.working} memos are being written out` : 'One memo is being written out'}</p>

      <section className="sb-sec" aria-label="Folders">
        <h4 className="sb-h">Folders<button type="button" className="btn ghost icon-only sm" onClick={() => setMaking(true)} aria-label="New folder" title="New folder"><Icon name="plus" size={14} /></button></h4>
        {folders.map(f => <FolderLink key={f.id} folder={f} onNavigate={onNavigate} />)}
        {!folders.length && <button type="button" className="sb-row sb-list sb-new" onClick={() => setMaking(true)}>
          <span className="sb-list-ico" aria-hidden="true"><Icon name="folder-plus" size={14} /></span><span className="grow ellipsis muted">New folder</span>
        </button>}
      </section>
      {making && <FolderNameModal onClose={() => setMaking(false)} onDone={f => { setMaking(false); onNavigate(); navigate(`/folder/${f.id}`) }} />}

      <section className="sb-sec">
        <h4 className="sb-h">About</h4>
        {KINDS.map(k => (
          <NavLink key={k.id} to={`/kind/${k.id}`} onClick={onNavigate} className={({ isActive }) => `sb-row sb-list ${isActive ? 'active' : ''}`}>
            <span className="sb-list-ico" aria-hidden="true"><Icon name={k.icon} size={14} /></span>
            <span className="grow ellipsis">{k.plural}</span>
            {!!kinds[k.id] && <span className="muted small tnum">{kinds[k.id]}</span>}
          </NavLink>
        ))}
      </section>

      {tags.length > 0 && <section className="sb-sec">
        <h4 className="sb-h">Tags</h4>
        {tags.slice(0, 10).map(t => (
          <NavLink key={t.tag} to={`/tag/${encodeURIComponent(t.tag)}`} onClick={onNavigate} className={({ isActive }) => `sb-row sb-list ${isActive ? 'active' : ''}`}>
            <span className="sb-list-ico" aria-hidden="true">#</span>
            <span className="grow ellipsis">{t.tag}</span>
            <span className="muted small tnum">{t.count}</span>
          </NavLink>
        ))}
      </section>}

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pvo:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

/** A folder in the sidebar; right-click to rename or remove it. */
function FolderLink({ folder: f, onNavigate }: { folder: Folder; onNavigate: () => void }) {
  const ctx = useContextMenu()
  const navigate = useNavigate()
  const [edit, setEdit] = useState<'' | 'rename' | 'delete'>('')
  return (<>
    <NavLink to={`/folder/${f.id}`} onClick={onNavigate} onContextMenu={ctx.open} className={({ isActive }) => `sb-row sb-list ${isActive ? 'active' : ''}`}>
      <span className="sb-list-ico" aria-hidden="true"><Icon name="folder" size={14} /></span>
      <span className="grow ellipsis">{f.name}</span>
      {!!f.count && <span className="muted small tnum" title={f.inbox ? `${f.count} memos, ${f.inbox} in the inbox` : `${f.count} memos`}>{f.count}</span>}
    </NavLink>
    {ctx.at && <ContextMenu at={ctx.at} onClose={ctx.close} items={[
      { label: 'Open', icon: 'chevron-right', onSelect: () => navigate(`/folder/${f.id}`) },
      { label: 'Rename…', icon: 'edit', onSelect: () => setEdit('rename') },
      'sep',
      { label: 'Remove folder', icon: 'trash', danger: true, onSelect: () => setEdit('delete') },
    ]} />}
    {edit === 'rename' && <FolderNameModal folder={f} onClose={() => setEdit('')} onDone={() => setEdit('')} />}
    {edit === 'delete' && <DeleteFolder folder={f} onClose={() => setEdit('')} onDone={() => { if (location.pathname === `/folder/${f.id}`) navigate('/memos') }} />}
  </>)
}
