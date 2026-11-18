// Folders: the person's own containers for memos and meetings. A memo is in
// at most one. While a folder is open, what is recorded, uploaded or typed
// goes into it.
import { useEffect } from 'react'
import { api, V1 } from './api'
import { announceChange } from './data'
import type { Folder } from './types'
import type { MenuItem } from '../components/ui'

let here = 0
/** The folder open now (0 = none): new memos go in it. */
export const folderHere = () => here
export function useFolderHere(id: number) {
  useEffect(() => { here = id; return () => { if (here === id) here = 0 } }, [id])
}

export const createFolder = (name: string) => api.post<Folder>(`${V1}/folders`, { name })

/** Puts memos in a folder (0 = in none). */
export async function moveToFolder(ids: number[], folderId: number) {
  const r = await api.post<{ moved: number }>(`${V1}/memos/folder`, { ids, folder_id: folderId })
  announceChange()
  return r.moved
}

/** "Move to folder" for a memo's menus: every folder, a new one, none. */
export function folderItems(current: number, folders: Folder[], move: (id: number, name: string) => void, make: () => void): MenuItem[] {
  return [
    ...folders.map(f => ({ label: f.name, icon: 'folder', checked: f.id === current, onSelect: () => move(f.id === current ? 0 : f.id, f.id === current ? '' : f.name) })),
    ...(folders.length ? ['sep' as const] : []),
    { label: 'New folder…', icon: 'folder-plus', onSelect: make },
    ...(current ? [{ label: 'Take out of the folder', icon: 'x', onSelect: () => move(0, '') }] : []),
  ]
}
