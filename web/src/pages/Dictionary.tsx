// The dictionary: words Voice should expect (names, how they are spelled) and
// replacements ("pocket ai" → "Pocket AI"), for memos and for dictation.
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Word } from '../lib/types'
import { plural } from '../lib/format'
import { useCrumb } from '../components/Layout'
import { Icon } from '../components/icons'
import { Empty, Loading, errMsg, useToast } from '../components/ui'

const fold = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()

export default function Dictionary() {
  useCrumb(undefined)
  const { toast } = useToast()
  const [items, setItems] = useState<Word[] | null>(null)
  const [err, setErr] = useState('')
  const [q, setQ] = useState('')
  const [phrase, setPhrase] = useState('')
  const [withText, setWithText] = useState('')
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState(0)

  const load = () => api.get<{ items: Word[] }>(`${V1}/dictionary`).then(r => { setItems(r.items); setErr('') }).catch(e => setErr(errMsg(e)))
  useEffect(() => { load() }, [])

  const add = async (e?: React.FormEvent) => {
    e?.preventDefault()
    if (!phrase.trim()) return
    setBusy(true)
    try {
      const r = await api.post<{ item: Word }>(`${V1}/dictionary`, { phrase, ...(withText.trim() ? { replace_with: withText } : {}) })
      setPhrase(''); setWithText('')
      toast(r.item.replace_with ? `“${r.item.phrase}” becomes “${r.item.replace_with}”` : `“${r.item.phrase}” added`)
      load()
    } catch (er) { toast(errMsg(er), 'err') } finally { setBusy(false) }
  }
  const save = async (w: Word, p: string, r: string) => {
    try { await api.put(`${V1}/dictionary/${w.id}`, { phrase: p, replace_with: r }); setEditing(0); load() } catch (er) { toast(errMsg(er), 'err') }
  }
  const remove = async (w: Word) => {
    try {
      await api.del(`${V1}/dictionary/${w.id}`)
      setItems(l => (l || []).filter(x => x.id !== w.id))
      toast(`“${w.phrase}” deleted`, 'ok', { label: 'Undo', run: async () => { try { await api.post(`${V1}/dictionary`, { phrase: w.phrase, replace_with: w.replace_with, source: w.source }); load() } catch (er) { toast(errMsg(er), 'err') } } })
    } catch (er) { toast(errMsg(er), 'err') }
  }

  const shown = useMemo(() => {
    const f = fold(q.trim())
    return (items || []).filter(w => !f || fold(w.phrase + ' ' + w.replace_with).includes(f))
  }, [items, q])
  const replacements = (items || []).filter(w => w.replace_with).length

  return (
    <div className="stack loose">
      <div className="page-head">
        <div className="grow">
          <h1 className="page-title">Dictionary{items && <span className="count muted">{items.length}</span>}</h1>
          <p className="muted small">Names and words Voice should expect, and words to replace. They are used when your memos are written out, in dictation from your Mac, and when dictation is polished.</p>
        </div>
      </div>

      <form className="card dict-add" onSubmit={add}>
        <label className="dict-add-f"><span className="label">Word or phrase</span><input value={phrase} onChange={e => setPhrase(e.target.value)} maxLength={100} placeholder="Amra, Kibe Mahala, pocket ai" aria-label="Word or phrase" /></label>
        <Icon name="chevron-right" size={16} className="muted dict-add-arrow" />
        <label className="dict-add-f"><span className="label">Replace with <span className="faint">(optional)</span></span><input value={withText} onChange={e => setWithText(e.target.value)} maxLength={200} placeholder="Pocket AI" aria-label="Replace with" /></label>
        <button type="submit" className="btn primary" disabled={busy || !phrase.trim()}><Icon name="plus" size={15} />Add</button>
        <p className="muted small dict-add-help">Without a replacement it is a word to expect: the speech model is told about it, so it is spelled your way. With one, whenever the phrase is heard (any capitals) it is written as the replacement.</p>
      </form>

      {items && items.length > 0 && <div className="row wrap filters">
        <div className="search-in input-icon"><Icon name="search" size={15} /><input type="search" value={q} onChange={e => setQ(e.target.value)} placeholder="Search the dictionary…" aria-label="Search the dictionary" /></div>
        <span className="muted small">{plural(items.length - replacements, 'word')} to expect · {plural(replacements, 'replacement')}</span>
      </div>}

      {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
      {!items && !err && <Loading />}
      {items && items.length === 0 && <div className="card"><Empty icon="dictionary" title="Your dictionary is empty">Add the names of the people and places you talk about, product names and words you spell your own way. <Link to="/settings/speech">Names and words to expect</Link> in Settings still work too.</Empty></div>}
      {items && items.length > 0 && shown.length === 0 && <Empty icon="search" title="Nothing found" />}
      {shown.length > 0 && <section className="card flush">
        <ul className="list">{shown.map(w => <li key={w.id}>{editing === w.id
          ? <WordEdit w={w} onSave={(p, r) => save(w, p, r)} onCancel={() => setEditing(0)} />
          : <div className="list-row word-row">
            <span className="grow word-main">
              <span className="word-phrase">{w.phrase}</span>
              {w.replace_with ? <><Icon name="chevron-right" size={14} className="muted" /><span className="word-with">{w.replace_with}</span></> : <span className="muted small">word to expect</span>}
              {w.source === 'learned' && <span className="pill accent" title="Learned from your corrections">learned</span>}
            </span>
            {w.replace_with && <span className="muted small tnum" title="How often it was replaced">{w.uses ? `${w.uses.toLocaleString()}×` : 'not yet'}</span>}
            <span className="word-actions">
              <button type="button" className="btn ghost icon-only sm" onClick={() => setEditing(w.id)} aria-label="Edit" title="Edit"><Icon name="edit" size={15} /></button>
              <button type="button" className="btn ghost icon-only sm dict-del" onClick={() => remove(w)} aria-label="Delete" title="Delete"><Icon name="trash" size={15} /></button>
            </span>
          </div>}</li>)}</ul>
      </section>}
    </div>
  )
}

function WordEdit({ w, onSave, onCancel }: { w: Word; onSave: (phrase: string, with_: string) => void; onCancel: () => void }) {
  const [p, setP] = useState(w.phrase)
  const [r, setR] = useState(w.replace_with)
  const key = (e: React.KeyboardEvent) => { if (e.key === 'Enter') { e.preventDefault(); onSave(p, r) } if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCancel() } }
  return (
    <div className="list-row word-row editing">
      <input value={p} onChange={e => setP(e.target.value)} onKeyDown={key} maxLength={100} autoFocus aria-label="Word or phrase" />
      <Icon name="chevron-right" size={14} className="muted" />
      <input value={r} onChange={e => setR(e.target.value)} onKeyDown={key} maxLength={200} placeholder="Replace with (optional)" aria-label="Replace with" />
      <button type="button" className="btn primary sm" onClick={() => onSave(p, r)} disabled={!p.trim()}>Save</button>
      <button type="button" className="btn sm" onClick={onCancel}>Cancel</button>
    </div>
  )
}
