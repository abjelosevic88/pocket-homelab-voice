// Naming a folder (new or renamed) and removing one.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { announceChange } from '../lib/data'
import { createFolder } from '../lib/folders'
import type { Folder } from '../lib/types'
import { Confirm, Field, Modal, useToast } from './ui'

/** Asks for a folder's name; makes it (or renames `folder`) and hands it back. */
export function FolderNameModal({ folder, onClose, onDone, hint }: { folder?: Folder; onClose: () => void; onDone: (f: Folder) => void; hint?: string }) {
  const { toast } = useToast()
  const [name, setName] = useState(folder?.name || '')
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      const f = folder ? await api.put<Folder>(`${V1}/folders/${folder.id}`, { name }) : await createFolder(name)
      announceChange()
      onDone(f)
    } catch (er) { toast((er as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={folder ? 'Rename folder' : 'New folder'} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid form-grid">
        <Field label="Name" help={hint || (folder ? undefined : 'Group memos and meetings that belong together: a project, a client, a weekly 1:1.')}>
          <input value={name} onChange={e => setName(e.target.value)} maxLength={80} required autoFocus placeholder="Project Atlas" />
        </Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy || !name.trim()}>{folder ? 'Rename' : 'Make the folder'}</button></div>
      </form>
    </Modal>
  )
}

export function DeleteFolder({ folder, onClose, onDone }: { folder: Folder; onClose: () => void; onDone: () => void }) {
  const { toast } = useToast()
  return <Confirm title={`Remove “${folder.name}”?`} confirmLabel="Remove folder" onCancel={onClose}
    message={folder.count ? `The ${folder.count === 1 ? 'memo' : `${folder.count} memos`} in it stay${folder.count === 1 ? 's' : ''}, in no folder.` : 'It is empty.'}
    onConfirm={async () => { onClose(); try { await api.del(`${V1}/folders/${folder.id}`); toast('Folder removed'); announceChange(); onDone() } catch (er) { toast((er as Error).message, 'err') } }} />
}
