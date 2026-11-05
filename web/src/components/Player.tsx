// The player: play/pause, the waveform you can tap anywhere on, ten seconds
// back and forward, and the speed. The words below follow along.
import { useEffect, useImperativeHandle, useRef, useState, forwardRef } from 'react'
import type { Memo } from '../lib/types'
import { clock } from '../lib/voice'
import { useApp } from '../lib/app-context'
import { Icon } from './icons'
import { useIsMobile } from './ui'

export interface PlayerHandle { seek: (ms: number, play?: boolean) => void; toggle: () => void }

const SPEEDS = [1, 1.25, 1.5, 2]

/** At most n bars: louder of neighbours wins, so short words stay visible. */
function fit(p: number[], n: number): number[] {
  if (p.length <= n) return p
  const out: number[] = []
  for (let i = 0; i < n; i++) {
    const a = Math.floor(i * p.length / n), b = Math.max(a + 1, Math.floor((i + 1) * p.length / n))
    out.push(Math.max(...p.slice(a, b)))
  }
  return out
}

export const Player = forwardRef<PlayerHandle, { memo: Memo; onTime?: (ms: number) => void }>(function Player({ memo, onTime }, ref) {
  const { settings } = useApp()
  const audio = useRef<HTMLAudioElement>(null)
  const wave = useRef<HTMLDivElement>(null)
  const [playing, setPlaying] = useState(false)
  const [t, setT] = useState(0)
  const [dur, setDur] = useState(memo.duration_ms)
  const [speed, setSpeed] = useState(() => Number(settings?.playback_speed || 1) || 1)
  const [failed, setFailed] = useState(false)
  const timeCb = useRef(onTime)
  timeCb.current = onTime

  useEffect(() => { setDur(memo.duration_ms) }, [memo.duration_ms])
  useEffect(() => { if (audio.current) audio.current.playbackRate = speed }, [speed])

  const seek = (ms: number, play = false) => {
    const a = audio.current
    if (!a) return
    a.currentTime = Math.max(0, ms / 1000)
    setT(ms)
    timeCb.current?.(ms)
    if (play) a.play().catch(() => {})
  }
  const toggle = () => {
    const a = audio.current
    if (!a) return
    if (a.paused) a.play().catch(() => setFailed(true))
    else a.pause()
  }
  useImperativeHandle(ref, () => ({ seek, toggle }))

  const mobile = useIsMobile()
  const peaks = fit(memo.peaks.length ? memo.peaks : Array.from({ length: 80 }, () => 6), mobile ? 56 : 120)
  const total = dur || memo.duration_ms || 1
  const at = Math.min(1, t / total)

  const fromPointer = (clientX: number) => {
    const r = wave.current!.getBoundingClientRect()
    return Math.max(0, Math.min(1, (clientX - r.left) / r.width)) * total
  }
  const drag = useRef(false)

  if (!memo.has_audio) {
    return (
      <div className="player off">
        <div className="wave" aria-hidden="true">{peaks.map((p, i) => <span key={i} style={{ height: `${Math.max(6, p)}%` }} />)}</div>
        <p className="muted small">{memo.audio_gone_at ? 'The recording was let go (Settings → Writing out → Keep recordings); the words stay.' : memo.engine === 'typed' ? 'Typed or dictated as text: there is no recording.' : 'There is no recording.'}</p>
      </div>
    )
  }
  return (
    <div className="player">
      <audio ref={audio} src={memo.audio_url} preload="metadata"
        onPlay={() => setPlaying(true)} onPause={() => setPlaying(false)} onEnded={() => setPlaying(false)} onError={() => setFailed(true)}
        onLoadedMetadata={e => { const d = e.currentTarget.duration; if (isFinite(d) && d > 0) setDur(Math.round(d * 1000)); e.currentTarget.playbackRate = speed }}
        onTimeUpdate={e => { const ms = e.currentTarget.currentTime * 1000; setT(ms); timeCb.current?.(ms) }} />
      <button type="button" className="play-btn" onClick={toggle} aria-label={playing ? 'Pause' : 'Play'} title={playing ? 'Pause (space)' : 'Play (space)'}>
        <Icon name={playing ? 'pause' : 'play'} size={22} strokeWidth={2} />
      </button>
      <div className="player-main">
        <div ref={wave} className="wave" role="slider" tabIndex={0} aria-label="Position" aria-valuemin={0} aria-valuemax={Math.round(total / 1000)} aria-valuenow={Math.round(t / 1000)} aria-valuetext={`${clock(t)} of ${clock(total)}`}
          onPointerDown={e => { drag.current = true; e.currentTarget.setPointerCapture(e.pointerId); seek(fromPointer(e.clientX)) }}
          onPointerMove={e => { if (drag.current) seek(fromPointer(e.clientX)) }}
          onPointerUp={() => { drag.current = false }}
          onKeyDown={e => { if (e.key === 'ArrowRight') { e.preventDefault(); seek(t + 5000) } else if (e.key === 'ArrowLeft') { e.preventDefault(); seek(t - 5000) } }}>
          {peaks.map((p, i) => <span key={i} className={i / peaks.length < at ? 'on' : ''} style={{ height: `${Math.max(6, p)}%` }} />)}
        </div>
        <div className="player-bar">
          <span className="tnum muted small">{clock(t)} / {clock(total)}</span>
          <span className="spacer" />
          <button type="button" className="btn ghost icon-only sm" onClick={() => seek(t - 10000)} aria-label="Back 10 seconds" title="Back 10 s"><Icon name="rotate-ccw" size={16} /></button>
          <button type="button" className="btn ghost icon-only sm" onClick={() => seek(t + 10000)} aria-label="Forward 10 seconds" title="Forward 10 s"><Icon name="rotate-cw" size={16} /></button>
          <button type="button" className="btn ghost sm speed-btn tnum" onClick={() => setSpeed(SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length] || 1)} title="Speed">{speed}×</button>
        </div>
        {failed && <p className="bad small">This browser cannot play the recording yet; it plays once it has been written out.</p>}
      </div>
    </div>
  )
})
