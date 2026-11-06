// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { announceChange } from '../lib/data'
import { useApp } from '../lib/app-context'
import { DataProvider } from '../lib/data'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { Recorder } from './Recorder'
import { TypeModal, UploadModal } from './Add'
import { Avatar, type MenuItem, MenuButton, modalOpen, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'
import { KINDS } from '../lib/voice'

export { announceChange, useOnChange } from '../lib/data'

const SIDEBAR_KEY = 'pvo-sidebar'

/** The page's own name for the top bar breadcrumb. */
const CrumbCtx = createContext<(t: string) => void>(() => {})
export function useCrumb(title: string | undefined) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || ''); return () => set('') }, [title, set])
}

export type CreateKind = 'record' | 'upload' | 'type'

/** Open the recorder or an add dialog from anywhere (a page's button). */
export const openCreate = (k: CreateKind) => window.dispatchEvent(new CustomEvent('pvo:create', { detail: { kind: k } }))

/** The add menu: the sidebar's caret. */
export function createItems(onCreate: (k: CreateKind) => void): MenuItem[] {
  return [
    { label: 'Record a memo', icon: 'mic', hint: 'R', onSelect: () => onCreate('record') },
    { label: 'Add recordings', icon: 'upload', hint: 'U', onSelect: () => onCreate('upload') },
    { label: 'Type a memo', icon: 'edit', hint: 'T', onSelect: () => onCreate('type') },
  ]
}

export default function Layout() {
  return <DataProvider><Shell /></DataProvider>
}

function Shell() {
  const navigate = useNavigate()
  const loc = useLocation()
  const [sp, setSp] = useSearchParams()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [crumb, setCrumbRaw] = useState('')
  const setCrumb = useCallback((t: string) => setCrumbRaw(t), [])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [modal, setModal] = useState<{ kind: CreateKind } | null>(null)

  const create = (k: CreateKind) => {
    closeDrawer()
    setModal({ kind: k })
  }
  const createRef = useRef(create)
  createRef.current = create

  // /?record=1 (the PWA shortcut, a Siri Shortcut) opens the recorder
  useEffect(() => { if (sp.get('record')) { createRef.current('record'); setSp(new URLSearchParams(), { replace: true }) } }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = (e: Event) => { const d = (e as CustomEvent<{ kind: CreateKind }>).detail; createRef.current(d.kind) }
    window.addEventListener('pvo:shortcuts', open)
    window.addEventListener('pvo:create', make)
    return () => { window.removeEventListener('pvo:shortcuts', open); window.removeEventListener('pvo:create', make) }
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      const mk = (k: CreateKind) => { e.preventDefault(); createRef.current(k) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'r': mk('record'); break
        case 'u': mk('upload'); break
        case 't': mk('type'); break
        case 'i': go('/'); break
        case 'm': go('/memos'); break
        case 's': go('/starred'); break
        case 'd': go('/dictation'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'record', label: 'Record a memo', icon: 'mic', hint: 'R', keywords: 'new voice dictate speak', run: () => create('record') },
    { id: 'upload', label: 'Add recordings', icon: 'upload', hint: 'U', keywords: 'import file apple voice memos whatsapp m4a mp3', run: () => create('upload') },
    { id: 'type', label: 'Type a memo', icon: 'edit', hint: 'T', keywords: 'text paste', run: () => create('type') },
    { id: 'inbox', label: 'Inbox', icon: 'inbox', hint: 'I', keywords: 'to file new', run: () => navigate('/') },
    { id: 'all', label: 'All memos', icon: 'list', hint: 'M', run: () => navigate('/memos') },
    { id: 'starred', label: 'Starred', icon: 'star', hint: 'S', run: () => navigate('/starred') },
    { id: 'dictation', label: 'Dictation history', icon: 'dictation', hint: 'D', keywords: 'launcher mac wispr typed words statistics stats saved', run: () => navigate('/dictation') },
    ...KINDS.map(k => ({ id: 'kind-' + k.id, label: k.plural, icon: k.icon, keywords: 'kind about', run: () => navigate(`/kind/${k.id}`) })),
    { id: 'dictionary', label: 'Dictionary', icon: 'dictionary', keywords: 'words names spelling replace replacements vocabulary', run: () => navigate('/dictionary') },
    { id: 'people', label: 'People', icon: 'users', keywords: 'voices speakers names meeting who', run: () => navigate('/people') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'dictation-settings', label: 'Settings: dictation styles', icon: 'dictation', keywords: 'style casual formal code polish app launcher', run: () => navigate('/settings/dictation') },
    { id: 'speech', label: 'Settings: writing out', icon: 'mic', keywords: 'whisper model language words transcribe', run: () => navigate('/settings/speech') },
    { id: 'apps', label: 'Settings: connected apps', icon: 'layers', keywords: 'notes tasks calendar money core', run: () => navigate('/settings/apps') },
    { id: 'notify', label: 'Settings: notifications', icon: 'bell', keywords: 'push', run: () => navigate('/settings/notifications') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  const close = () => setModal(null)
  return (
    <CrumbCtx.Provider value={setCrumb}>
      <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
        {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
        <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
          <Sidebar onCreate={create} onNavigate={closeDrawer} />
        </div>
        <div className="main">
          <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
          <main className="content"><div className="content-inner"><Outlet /></div></main>
        </div>
        {mobile && <button type="button" className="fab" aria-label="Record a memo" title="Record a memo" onClick={() => create('record')}><Icon name="mic" size={24} strokeWidth={2.2} /></button>}
        {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
        {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
        {modal?.kind === 'record' && <Recorder onClose={close} onSaved={m => { close(); announceChange(); navigate(`/memos/${m.id}`) }} />}
        {modal?.kind === 'upload' && <UploadModal onClose={close} onDone={m => { close(); announceChange(); navigate(m ? `/memos/${m.id}` : '/') }} />}
        {modal?.kind === 'type' && <TypeModal onClose={close} onDone={m => { close(); announceChange(); navigate(`/memos/${m.id}`) }} />}
      </div>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = { '': ['Inbox', '/'], inbox: ['Inbox', '/'], memos: ['Memos', '/memos'], starred: ['Starred', '/starred'], kind: ['Memos', '/memos'], tag: ['Memos', '/memos'], dictation: ['Dictation', '/dictation'], dictionary: ['Dictionary', '/dictionary'], people: ['People', '/people'] }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = SECTION[loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pvo:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb ? <><button type="button" className="crumb" onClick={() => navigate(sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb}</span></> : section}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
