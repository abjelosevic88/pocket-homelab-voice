// Tags as badges: each one links to its memos and has an × to take it off;
// type to add (Enter, comma or Tab), Backspace on an empty field takes the
// last one off, and the tags you already use are suggested as you type.
import { useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useData } from '../lib/data'
import { Icon } from './icons'

const clean = (t: string) => t.trim().replace(/^#/, '').replace(/\s+/g, ' ').toLowerCase()
export const splitTags = (s: string) => [...new Set(s.split(',').map(clean).filter(Boolean))]

export function TagInput({ value, onChange, placeholder = 'Add a tag' }: { value: string; onChange: (tags: string) => void; placeholder?: string }) {
  const { tags: known } = useData()
  const tags = splitTags(value)
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [hi, setHi] = useState(0)
  const input = useRef<HTMLInputElement>(null)

  const set = (list: string[]) => onChange(list.join(', '))
  const add = (raw: string) => {
    const more = splitTags(raw).filter(t => !tags.includes(t))
    if (more.length) set([...tags, ...more])
    setText(''); setHi(0)
  }
  const remove = (t: string) => { set(tags.filter(x => x !== t)); input.current?.focus() }

  const q = clean(text)
  const suggest = useMemo(() => known.map(k => k.tag).filter(t => !tags.includes(t) && (!q || t.includes(q)))
    .sort((a, b) => Number(!a.startsWith(q)) - Number(!b.startsWith(q))).slice(0, 6), [known, tags, q])
  const shown = open && suggest.length > 0

  const key = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown' && shown) { e.preventDefault(); setHi(h => (h + 1) % suggest.length) }
    else if (e.key === 'ArrowUp' && shown) { e.preventDefault(); setHi(h => (h - 1 + suggest.length) % suggest.length) }
    else if (e.key === 'Enter' || e.key === ',' || (e.key === 'Tab' && q)) {
      if (!q && !(shown && e.key === 'Enter')) return
      e.preventDefault()
      add(shown && (e.key === 'Enter' || e.key === 'Tab') && (!q || suggest[hi]?.startsWith(q)) ? suggest[hi] : text)
    } else if (e.key === 'Backspace' && !text && tags.length) { e.preventDefault(); set(tags.slice(0, -1)) }
    else if (e.key === 'Escape' && shown) { e.preventDefault(); e.stopPropagation(); setOpen(false) }
  }

  return (
    <div className="tag-input" onClick={e => { if (e.target === e.currentTarget) input.current?.focus() }}>
      {tags.map(t => (
        <span key={t} className="tag-badge">
          <Link to={`/tag/${encodeURIComponent(t)}`} title={`Memos tagged ${t}`}>#{t}</Link>
          <button type="button" onClick={() => remove(t)} aria-label={`Take off ${t}`} title="Take off"><Icon name="x" size={12} strokeWidth={2.4} /></button>
        </span>
      ))}
      <span className="tag-entry">
        <input ref={input} value={text} placeholder={tags.length ? '' : placeholder} aria-label="Add a tag" maxLength={40}
          onChange={e => { const v = e.target.value; if (v.includes(',')) add(v); else { setText(v); setHi(0); setOpen(true) } }}
          onPaste={e => { const v = e.clipboardData.getData('text'); if (v.includes(',')) { e.preventDefault(); add(text + v) } }}
          onFocus={() => setOpen(true)} onBlur={() => { setOpen(false); if (q) add(text) }} onKeyDown={key} />
        {shown && <ul className="tag-suggest" role="listbox">{suggest.map((t, i) => (
          <li key={t} role="option" aria-selected={i === hi} className={i === hi ? 'on' : ''}
            onMouseDown={e => { e.preventDefault(); add(t) }} onMouseEnter={() => setHi(i)}>#{t}<span className="muted small">{known.find(k => k.tag === t)?.count}</span></li>
        ))}</ul>}
      </span>
    </div>
  )
}
