// The player: play/pause, the waveform — tap to jump, drag across it to pick a
// part — ten seconds back and forward, and the speed. In a meeting the bars
// take the colour of who was talking, a lane per voice under them shows
// exactly where each spoke, and a chip per voice shows how much each talked;
// tap one to light up only their turns and step through them. The words
// below follow along.
import { useEffect, useImperativeHandle, useMemo, useRef, useState, forwardRef } from 'react'
import type { Memo } from '../lib/types'
import { clock } from '../lib/voice'
import { hasSpeakers, speakersAt, speakerSpans, voices, within, type Range, type Who } from '../lib/part'
import { useApp } from '../lib/app-context'
import { Icon } from './icons'
import { useIsMobile } from './ui'

export interface PlayerHandle { seek: (ms: number, play?: boolean) => void; toggle: () => void; playRange: (r: Range) => void }

const SPEEDS = [1, 1.25, 1.5, 2]
const EDGE_PX = 10 // grab a part's edge within this many pixels
const MIN_PART = 1000

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

type Grab = { x0: number; t0: number; mode: 'tap' | 'pick' | 'from' | 'to' }

export const Player = forwardRef<PlayerHandle, {
  memo: Memo; onTime?: (ms: number) => void
  range?: Range | null; onRange?: (r: Range | null) => void
  focus?: Who; onFocus?: (w: Who) => void
}>(function Player({ memo, onTime, range = null, onRange, focus = '', onFocus }, ref) {
  const { settings } = useApp()
  const audio = useRef<HTMLAudioElement>(null)
  const wave = useRef<HTMLDivElement>(null)
  const [playing, setPlaying] = useState(false)
  const [t, setT] = useState(0)
  const [dur, setDur] = useState(memo.duration_ms)
  const [speed, setSpeed] = useState(() => Number(settings?.playback_speed || 1) || 1)
  const [failed, setFailed] = useState(false)
  const [hover, setHover] = useState<number | null>(null)
  const [dragging, setDragging] = useState(false)
  const timeCb = useRef(onTime)
  timeCb.current = onTime
  const stopAt = useRef<number | null>(null)

  useEffect(() => { setDur(memo.duration_ms) }, [memo.duration_ms])
  useEffect(() => { if (audio.current) audio.current.playbackRate = speed }, [speed])

  const seek = (ms: number, play = false) => {
    const a = audio.current
    if (!a) return
    stopAt.current = null
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
  const playRange = (r: Range) => { seek(r.from, true); stopAt.current = r.to }
  useImperativeHandle(ref, () => ({ seek, toggle, playRange }))

  const mobile = useIsMobile()
  const peaks = fit(memo.peaks.length ? memo.peaks : Array.from({ length: 80 }, () => 6), mobile ? 56 : 120)
  const total = dur || memo.duration_ms || 1
  const at = Math.min(1, t / total)
  const pct = (ms: number) => `${Math.max(0, Math.min(100, ms / total * 100))}%`

  // who talked when: stretches per speaker, each bar's colour, each speaker's share
  const meeting = hasSpeakers(memo.segments)
  const spans = useMemo(() => speakerSpans(memo.segments), [memo.segments])
  const turns = useMemo(() => speakerSpans(memo.segments, 4000), [memo.segments])
  const bars = peaks.length
  const vs = useMemo(() => voices(memo), [memo])
  const color = useMemo(() => Object.fromEntries(vs.map(v => [v.key, v.color])), [vs])
  const label = (k: string) => vs.find(v => v.key === k)?.label || k
  // each bar: who talked most in its stretch
  const barWho = useMemo(() => {
    if (!meeting) return []
    const step = total / bars
    return Array.from({ length: bars }, (_, i) => {
      let best = '', most = 0
      for (const v of vs) {
        const ms = within(spans[v.key], i * step, (i + 1) * step)
        if (ms > most) { best = v.key; most = ms }
      }
      return best
    })
  }, [meeting, spans, total, bars, vs])
  const talked = useMemo(() => Object.fromEntries(vs.map(v => [v.key, within(spans[v.key], 0, Infinity)])), [spans, vs])
  const talkTotal = Object.values(talked).reduce((a, b) => a + b, 0) || 1

  const fromX = (clientX: number) => {
    const r = wave.current!.getBoundingClientRect()
    return Math.max(0, Math.min(1, (clientX - r.left) / r.width)) * total
  }
  const xOf = (ms: number) => {
    const r = wave.current!.getBoundingClientRect()
    return r.left + ms / total * r.width
  }
  const grab = useRef<Grab | null>(null)
  const set = (a: number, b: number) => onRange?.({ from: Math.round(Math.max(0, Math.min(a, b))), to: Math.round(Math.min(total, Math.max(a, b))) })

  const down = (e: React.PointerEvent) => {
    e.currentTarget.setPointerCapture(e.pointerId)
    let mode: Grab['mode'] = 'tap'
    if (range && onRange) {
      const df = Math.abs(e.clientX - xOf(range.from)), dt = Math.abs(e.clientX - xOf(range.to))
      if (Math.min(df, dt) <= EDGE_PX) mode = df <= dt ? 'from' : 'to'
    }
    grab.current = { x0: e.clientX, t0: fromX(e.clientX), mode }
    if (mode !== 'tap') setDragging(true)
  }
  const move = (e: React.PointerEvent) => {
    const ms = fromX(e.clientX)
    if (e.pointerType === 'mouse') setHover(ms)
    const g = grab.current
    if (!g) return
    if (g.mode === 'tap' && onRange && Math.abs(e.clientX - g.x0) > 5) { g.mode = 'pick'; setDragging(true) }
    if (g.mode === 'pick') set(g.t0, ms)
    else if (g.mode === 'from' && range) set(ms, range.to)
    else if (g.mode === 'to' && range) set(range.from, ms)
  }
  const up = (e: React.PointerEvent) => {
    const g = grab.current
    grab.current = null
    setDragging(false)
    if (!g) return
    if (g.mode === 'tap') { seek(g.t0); return }
    const ms = fromX(e.clientX)
    const r = g.mode === 'pick' ? { from: Math.min(g.t0, ms), to: Math.max(g.t0, ms) } : range
    if (!r || r.to - r.from < MIN_PART) { onRange?.(null); seek(g.t0); return }
    if (g.mode === 'pick') seek(r.from)
  }

  // step through the lit-up speaker's turns
  const step = (dir: 1 | -1) => {
    const list = turns[focus] || []
    const next = dir > 0 ? list.find(x => x.s > t + 500) : [...list].reverse().find(x => x.s < t - 1500)
    if (next) seek(next.s, true)
  }

  if (!memo.has_audio) {
    return (
      <div className="player off">
        <div className="wave" aria-hidden="true">{peaks.map((p, i) => <span key={i} style={{ height: `${Math.max(6, p)}%` }} />)}</div>
        <p className="muted small">{memo.audio_gone_at ? 'The recording was let go (Settings → Writing out → Keep recordings); the words stay.' : memo.engine === 'typed' ? 'Typed or dictated as text: there is no recording.' : 'There is no recording.'}</p>
      </div>
    )
  }
  const hoverWho = hover !== null && meeting ? speakersAt(spans, hover).map(label).join(' + ') : ''
  return (
    <div className={`player ${meeting ? 'has-talk' : ''} ${focus ? 'focused' : ''}`}>
      <audio ref={audio} src={memo.audio_url} preload="metadata"
        onPlay={() => setPlaying(true)} onPause={() => setPlaying(false)} onEnded={() => setPlaying(false)} onError={() => setFailed(true)}
        onLoadedMetadata={e => { const d = e.currentTarget.duration; if (isFinite(d) && d > 0) setDur(Math.round(d * 1000)); e.currentTarget.playbackRate = speed }}
        onTimeUpdate={e => {
          const ms = e.currentTarget.currentTime * 1000
          if (stopAt.current !== null && ms >= stopAt.current) { e.currentTarget.pause(); stopAt.current = null }
          setT(ms); timeCb.current?.(ms)
        }} />
      <button type="button" className="play-btn" onClick={toggle} aria-label={playing ? 'Pause' : 'Play'} title={playing ? 'Pause (space)' : 'Play (space)'}>
        <Icon name={playing ? 'pause' : 'play'} size={22} strokeWidth={2} />
      </button>
      <div className="player-main">
        <div ref={wave} className={`scrub ${dragging ? 'dragging' : ''}`} role="slider" tabIndex={0} aria-label="Position — drag across to pick a part" aria-valuemin={0} aria-valuemax={Math.round(total / 1000)} aria-valuenow={Math.round(t / 1000)} aria-valuetext={`${clock(t)} of ${clock(total)}`}
          onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerCancel={() => { grab.current = null; setDragging(false) }} onPointerLeave={() => setHover(null)}
          onKeyDown={e => {
            if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
            e.preventDefault()
            const to = Math.max(0, Math.min(total, t + (e.key === 'ArrowRight' ? 5000 : -5000)))
            // shift+arrows stretch a part from where you are
            if (e.shiftKey && onRange) set(range ? (Math.abs(range.from - t) < Math.abs(range.to - t) ? range.to : range.from) : t, to)
            seek(to)
          }}>
          <div className="wave">
            {peaks.map((p, i) => {
              const k = barWho[i]
              return <span key={i} className={`${i / bars < at ? 'on' : ''} ${k ? 'talk' : ''} ${focus && k !== focus ? 'dim' : ''}`}
                style={{ height: `${Math.max(6, p)}%`, ...(k ? { '--c': color[k] } : {}) } as React.CSSProperties} />
            })}
          </div>
          {meeting && <div className="lanes" aria-hidden="true">
            {vs.map(v => <div key={v.key} className={`lane ${focus === v.key ? 'lit' : focus ? 'dim' : ''}`} style={{ '--c': v.color } as React.CSSProperties} title={v.label}>
              {(spans[v.key] || []).map((x, i) => <i key={i} style={{ left: pct(x.s), width: pct(x.e - x.s) }} />)}</div>)}
          </div>}
          {range && <div className="part-sel" style={{ left: pct(range.from), width: pct(range.to - range.from) }}><i className="edge" /><i className="edge r" /></div>}
          <div className="playhead" style={{ left: pct(t) }} />
          {hover !== null && !dragging && <div className="hover-at" style={{ left: pct(hover) }}><span className="tnum">{clock(hover)}{hoverWho ? ` · ${hoverWho}` : ''}</span></div>}
        </div>
        {meeting && <div className="talk-legend">
          {vs.map(v => {
            const ms = talked[v.key] || 0
            return (
              <button key={v.key} type="button" className={`talk-chip ${focus === v.key ? 'on' : ''} ${v.speaker && v.speaker.state !== 'you' && !v.speaker.name ? 'unnamed' : ''}`} aria-pressed={focus === v.key}
                style={{ '--c': v.color } as React.CSSProperties}
                onClick={() => onFocus?.(focus === v.key ? '' : v.key)} title={focus === v.key ? 'Show everyone' : v.key === 'You' ? 'Light up where you spoke' : `Light up where ${v.label} spoke`}>
                <i />{v.label}<b className="tnum">{Math.round(ms / talkTotal * 100)}%</b><span className="muted tnum">{clock(ms)}</span>
              </button>
            )
          })}
          {focus && <span className="row turn-step">
            <button type="button" className="btn ghost icon-only sm" onClick={() => step(-1)} aria-label={`${focus}: previous turn`} title="Previous turn"><Icon name="chevron-left" size={16} /></button>
            <span className="muted small tnum">{(turns[focus] || []).length} turns</span>
            <button type="button" className="btn ghost icon-only sm" onClick={() => step(1)} aria-label={`${focus}: next turn`} title="Next turn"><Icon name="chevron-right" size={16} /></button>
          </span>}
        </div>}
        <div className="player-bar">
          <span className="tnum muted small">{clock(t)} / {clock(total)}</span>
          {!range && onRange && !mobile && <span className="muted small pick-hint">· drag across the recording to pick a part</span>}
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
