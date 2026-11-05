// The person's counts (inbox, starred, being written out), kinds and tags,
// loaded once for the sidebar; any change in the app ("pvo:changed")
// reloads them, and while a memo is being written out they refresh calmly.
import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { api, V1 } from './api'
import type { Counts, TagCount } from './types'

interface Data { counts: Counts | null; kinds: Record<string, number>; tags: TagCount[]; reload: () => void }
const Ctx = createContext<Data>({ counts: null, kinds: {}, tags: [], reload: () => {} })
export const useData = () => useContext(Ctx)

/** Something was added or changed: lists and pages reload. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('pvo:changed'))
export function useOnChange(cb: () => void) {
  const ref = React.useRef(cb)
  ref.current = cb
  useEffect(() => {
    const h = () => ref.current()
    window.addEventListener('pvo:changed', h)
    return () => window.removeEventListener('pvo:changed', h)
  }, [])
}

export function DataProvider({ children }: { children: React.ReactNode }) {
  const [counts, setCounts] = useState<Counts | null>(null)
  const [kinds, setKinds] = useState<Record<string, number>>({})
  const [tags, setTags] = useState<TagCount[]>([])
  const reload = useCallback(() => {
    api.get<{ counts: Counts }>(`${V1}/memos?limit=1`).then(r => setCounts(r.counts)).catch(() => {})
    api.get<{ counts: Record<string, number> }>(`${V1}/kinds`).then(r => setKinds(r.counts || {})).catch(() => {})
    api.get<TagCount[]>(`${V1}/tags`).then(setTags).catch(() => {})
  }, [])
  useEffect(() => { reload() }, [reload])
  useOnChange(reload)
  const working = useRef(0)
  working.current = counts?.working || 0
  useEffect(() => {
    const t = window.setInterval(() => { if (working.current > 0 && !document.hidden) reload() }, 4000)
    return () => window.clearInterval(t)
  }, [reload])
  return <Ctx.Provider value={{ counts, kinds, tags, reload }}>{children}</Ctx.Provider>
}
