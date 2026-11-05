// The recorder: the microphone, a calm level line, pause and resume, and the
// recording sent to the server when you stop. A recording that cannot be
// sent is never thrown away: try again, or save the file.
import { useEffect, useRef, useState } from 'react'
import { V1 } from '../lib/api'
import type { Memo } from '../lib/types'
import { clock } from '../lib/voice'
import { Icon } from './icons'
import { Confirm, Modal } from './ui'

type Phase = 'starting' | 'recording' | 'paused' | 'sending' | 'failed' | 'denied'

const TYPES = ['audio/webm;codecs=opus', 'audio/mp4', 'audio/ogg;codecs=opus', 'audio/webm']

function pickType(): string {
  const MR = window.MediaRecorder
  if (!MR?.isTypeSupported) return ''
  return TYPES.find(t => MR.isTypeSupported(t)) || ''
}

function extOf(type: string): string {
  if (type.includes('mp4')) return '.m4a'
  if (type.includes('ogg')) return '.ogg'
  return '.webm'
}

/** Sends a file as a new memo with upload progress. */
export function sendMemo(file: Blob, name: string, fields: Record<string, string>, onProgress?: (pct: number) => void): Promise<Memo> {
  return new Promise((resolve, reject) => {
    const fd = new FormData()
    for (const [k, v] of Object.entries(fields)) fd.append(k, v)
    fd.append('file', file, name)
    const x = new XMLHttpRequest()
    x.open('POST', `${V1}/memos`)
    x.withCredentials = true
    x.upload.onprogress = e => { if (e.lengthComputable && onProgress) onProgress(Math.round(e.loaded * 100 / e.total)) }
    x.onload = () => {
      let j: Record<string, unknown> = {}
      try { j = JSON.parse(x.responseText) } catch { /* not JSON */ }
      if (x.status === 401) window.dispatchEvent(new CustomEvent('pvo:unauthorized'))
      if (x.status >= 200 && x.status < 300) resolve(j as unknown as Memo)
      else reject(new Error((j.error as string) || `HTTP ${x.status}`))
    }
    x.onerror = () => reject(new Error('The server could not be reached'))
    x.send(fd)
  })
}

export function Recorder({ onClose, onSaved }: { onClose: () => void; onSaved: (m: Memo) => void }) {
  const [phase, setPhase] = useState<Phase>('starting')
  const [err, setErr] = useState('')
  const [ms, setMs] = useState(0)
  const [pct, setPct] = useState(0)
  const [askCancel, setAskCancel] = useState(false)
  const rec = useRef<MediaRecorder | null>(null)
  const stream = useRef<MediaStream | null>(null)
  const chunks = useRef<Blob[]>([])
  const blob = useRef<Blob | null>(null)
  const started = useRef('')
  const acc = useRef(0) // ms recorded before the last resume
  const since = useRef(0)
  const canvas = useRef<HTMLCanvasElement>(null)
  const levels = useRef<number[]>([])
  const raf = useRef(0)
  const ctx = useRef<AudioContext | null>(null)
  const lock = useRef<{ release: () => Promise<void> } | null>(null)
  const type = useRef(pickType())

  const elapsed = () => acc.current + (rec.current?.state === 'recording' ? performance.now() - since.current : 0)

  useEffect(() => {
    let dead = false
    const start = async () => {
      if (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia) {
        setPhase('denied'); setErr('The microphone works only when Voice is opened over https.'); return
      }
      if (!window.MediaRecorder) { setPhase('denied'); setErr('This browser cannot record audio.'); return }
      try {
        const s = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } })
        if (dead) { s.getTracks().forEach(t => t.stop()); return }
        stream.current = s
        const r = new MediaRecorder(s, type.current ? { mimeType: type.current, audioBitsPerSecond: 64000 } : undefined)
        type.current = r.mimeType || type.current
        r.ondataavailable = e => { if (e.data.size) chunks.current.push(e.data) }
        rec.current = r
        r.start(1000)
        started.current = new Date().toISOString()
        since.current = performance.now()
        setPhase('recording')
        meter(s)
        try { lock.current = await (navigator as unknown as { wakeLock?: { request: (t: string) => Promise<{ release: () => Promise<void> }> } }).wakeLock?.request('screen') || null } catch { /* fine */ }
      } catch (e) {
        const name = (e as DOMException)?.name
        setPhase('denied')
        const framed = window.self !== window.top
        setErr(name === 'NotAllowedError' ? (framed ? 'The page Voice is shown in does not let it use the microphone. Open Pocket Voice in its own tab to record.' : 'Voice may not use the microphone. Allow it in the browser’s settings for this site, then try again.')
          : name === 'NotFoundError' ? 'No microphone was found.' : (e as Error).message)
      }
    }
    start()
    const tick = window.setInterval(() => setMs(elapsed()), 250)
    return () => { dead = true; window.clearInterval(tick); stopAll() }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const meter = (s: MediaStream) => {
    try {
      const AC = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
      const ac = new AC()
      ctx.current = ac
      const src = ac.createMediaStreamSource(s)
      const an = ac.createAnalyser()
      an.fftSize = 1024
      src.connect(an)
      const buf = new Float32Array(an.fftSize)
      let last = 0
      const draw = (t: number) => {
        raf.current = requestAnimationFrame(draw)
        if (t - last < 66) return // 15 frames a second is plenty
        last = t
        an.getFloatTimeDomainData(buf)
        let sum = 0
        for (const v of buf) sum += v * v
        const lvl = rec.current?.state === 'recording' ? Math.min(1, Math.sqrt(sum / buf.length) * 4) : 0
        levels.current.push(lvl)
        if (levels.current.length > 64) levels.current.shift()
        paint()
      }
      raf.current = requestAnimationFrame(draw)
    } catch { /* no meter, still records */ }
  }

  const paint = () => {
    const c = canvas.current
    if (!c) return
    const dpr = window.devicePixelRatio || 1
    const w = c.clientWidth, h = c.clientHeight
    if (c.width !== w * dpr) { c.width = w * dpr; c.height = h * dpr }
    const g = c.getContext('2d')!
    g.setTransform(dpr, 0, 0, dpr, 0, 0)
    g.clearRect(0, 0, w, h)
    const col = getComputedStyle(c).color
    g.fillStyle = col
    const n = 64, gap = 3, bw = (w - gap * (n - 1)) / n
    const lv = levels.current
    for (let i = 0; i < n; i++) {
      const v = lv[lv.length - n + i] ?? 0
      const bh = Math.max(3, Math.sqrt(v) * h)
      g.globalAlpha = 0.35 + 0.65 * (i / n)
      g.beginPath()
      g.roundRect(i * (bw + gap), (h - bh) / 2, bw, bh, bw / 2)
      g.fill()
    }
  }

  const stopAll = () => {
    cancelAnimationFrame(raf.current)
    try { if (rec.current && rec.current.state !== 'inactive') rec.current.stop() } catch { /* already */ }
    stream.current?.getTracks().forEach(t => t.stop())
    ctx.current?.close().catch(() => {})
    lock.current?.release().catch(() => {})
    lock.current = null
  }

  const pause = () => {
    const r = rec.current
    if (!r) return
    if (r.state === 'recording') { r.pause(); acc.current += performance.now() - since.current; setPhase('paused') }
    else if (r.state === 'paused') { r.resume(); since.current = performance.now(); setPhase('recording') }
  }

  const finish = () => {
    const r = rec.current
    if (!r) return
    const dur = Math.round(elapsed())
    acc.current = dur
    setMs(dur)
    r.onstop = () => {
      blob.current = new Blob(chunks.current, { type: type.current || 'audio/webm' })
      stopAll()
      send()
    }
    if (r.state === 'paused') r.resume()
    r.stop()
    setPhase('sending')
  }

  const send = async () => {
    if (!blob.current) return
    setPhase('sending'); setErr(''); setPct(0)
    try {
      const m = await sendMemo(blob.current, 'memo' + extOf(type.current), { recorded_at: started.current, duration_ms: String(Math.round(acc.current)), from: 'record' }, setPct)
      onSaved(m)
    } catch (e) { setErr((e as Error).message); setPhase('failed') }
  }

  const saveFile = () => {
    if (!blob.current) return
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob.current)
    a.download = `memo-${started.current.slice(0, 16).replace(/[:T]/g, '-')}${extOf(type.current)}`
    a.click()
    setTimeout(() => URL.revokeObjectURL(a.href), 5000)
  }

  const cancel = () => {
    if ((phase === 'recording' || phase === 'paused') && elapsed() > 5000) { setAskCancel(true); return }
    stopAll(); onClose()
  }

  const live = phase === 'recording' || phase === 'paused'
  return (
    <Modal bare onClose={cancel} className="rec-modal" labelledBy="rec-title">
      <div className="rec">
        <div className="rec-head">
          <h2 id="rec-title">{phase === 'paused' ? 'Paused' : phase === 'sending' ? 'Saving…' : phase === 'failed' ? 'Not saved yet' : phase === 'denied' ? 'No microphone' : 'Recording'}</h2>
          <button type="button" className="btn ghost icon-only" onClick={cancel} aria-label="Close"><Icon name="x" size={18} /></button>
        </div>
        <div className={`rec-clock tnum ${phase === 'recording' ? 'on' : ''}`} aria-live="off">{clock(ms)}</div>
        <canvas ref={canvas} className={`rec-level ${phase === 'paused' ? 'paused' : ''}`} aria-hidden="true" />
        {phase === 'starting' && <p className="muted center">Asking for the microphone…</p>}
        {phase === 'denied' && <p className="bad center">{err}</p>}
        {phase === 'sending' && <div className="rec-send"><div className="bar"><span style={{ width: `${pct}%` }} /></div><p className="muted small center">Sending the recording… {pct}%</p></div>}
        {phase === 'failed' && <div className="callout danger"><Icon name="alert" size={15} /><div>{err}. The recording is still here: try again, or save the file and add it later.</div></div>}
        <div className="rec-controls">
          {live && <>
            <button type="button" className="btn lg rec-side" onClick={pause} aria-label={phase === 'paused' ? 'Resume' : 'Pause'}><Icon name={phase === 'paused' ? 'mic' : 'pause'} size={20} />{phase === 'paused' ? 'Resume' : 'Pause'}</button>
            <button type="button" className="rec-stop" onClick={finish} aria-label="Stop and save" title="Stop and save"><span /></button>
            <button type="button" className="btn lg ghost rec-side" onClick={cancel}>Cancel</button>
          </>}
          {phase === 'failed' && <>
            <button type="button" className="btn primary" onClick={send}><Icon name="refresh" size={15} />Try again</button>
            <button type="button" className="btn" onClick={saveFile}><Icon name="download" size={15} />Save the file</button>
          </>}
          {phase === 'denied' && <>
            {window.self !== window.top && <a className="btn primary" href={`${window.location.origin}/?record=1`} target="_blank" rel="noreferrer"><Icon name="external" size={15} />Open in its own tab</a>}
            <button type="button" className="btn" onClick={onClose}>Close</button>
          </>}
        </div>
        {live && <p className="muted small center rec-hint">Stop to save; it is written out on your own server. The screen stays on while you record.</p>}
      </div>
      {askCancel && <Confirm title="Throw this recording away?" message={`${clock(ms)} of recording will be lost.`} confirmLabel="Throw away" onCancel={() => setAskCancel(false)} onConfirm={() => { setAskCancel(false); stopAll(); onClose() }} />}
    </Modal>
  )
}
