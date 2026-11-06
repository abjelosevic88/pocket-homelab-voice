// Who spoke when, and parts of a recording: the stretches each speaker talked
// (for the lanes under the waveform), how long each of them talked in a part,
// and a part as it goes in the address (?part=750-1125, in seconds).
import type { Memo, Segment, Speaker } from './types'
import { clock } from './voice'

export interface Range { from: number; to: number }
/** A speaker's key ('You', 'S1', …; 'Others' in meetings written before voices were told apart), '' = everyone. */
export type Who = string
export interface Span { s: number; e: number }

export const hasSpeakers = (segs: Segment[]) => segs.some(s => s.speaker)

/** A voice as shown: its label, its colour, and the speaker behind it. */
export interface Voice { key: string; label: string; color: string; speaker?: Speaker }

const YOU = '#3b82f6'
const PALETTE = ['#f97316', '#22c55e', '#a855f7', '#06b6d4', '#eab308', '#ec4899', '#6366f1', '#84cc16', '#ef4444', '#14b8a6']

export const speakerName = (sp: Speaker) => sp.key === 'You' ? 'You' : sp.name || `Speaker ${sp.key.replace(/^S/, '')}`

/** The voices of a memo, you first, each with its colour. */
export function voices(m: Memo): Voice[] {
  if (m.speakers?.length) {
    let i = 0
    return m.speakers.map(sp => ({ key: sp.key, label: speakerName(sp), speaker: sp, color: sp.key === 'You' ? YOU : PALETTE[i++ % PALETTE.length] }))
  }
  const keys = [...new Set(m.segments.map(s => s.speaker || '').filter(Boolean))].sort((a, b) => (a === 'You' ? -1 : b === 'You' ? 1 : a.localeCompare(b)))
  let i = 0
  return keys.map(k => ({ key: k, label: k, color: k === 'You' ? YOU : PALETTE[i++ % PALETTE.length] }))
}

/** Each speaker's segments joined into stretches; gaps shorter than `gap` ms close up. */
export function speakerSpans(segs: Segment[], gap = 1200): Record<string, Span[]> {
  const out: Record<string, Span[]> = {}
  for (const sg of [...segs].sort((a, b) => a.s - b.s)) {
    if (!sg.speaker || sg.e <= sg.s) continue
    const l = (out[sg.speaker] ||= [])
    const last = l[l.length - 1]
    if (last && sg.s - last.e <= gap) last.e = Math.max(last.e, sg.e)
    else l.push({ s: sg.s, e: sg.e })
  }
  return out
}

/** Milliseconds of `spans` inside [from, to). */
export function within(spans: Span[] | undefined, from: number, to: number): number {
  let ms = 0
  for (const x of spans || []) ms += Math.max(0, Math.min(x.e, to) - Math.max(x.s, from))
  return ms
}

/** The segments (with their index) that overlap [from, to). */
export function inRange(segs: Segment[], r: Range): [Segment, number][] {
  const out: [Segment, number][] = []
  segs.forEach((s, i) => { if (s.e > r.from && s.s < r.to && s.t.trim()) out.push([s, i]) })
  return out
}

/** The keys of who was talking at `ms`. */
export function speakersAt(spans: Record<string, Span[]>, ms: number): string[] {
  return Object.keys(spans).filter(k => spans[k].some(x => ms >= x.s && ms < x.e))
}

/** The part as words to paste: "[12:30] You: …", one line per turn. */
export function partText(segs: Segment[], label: (key: string) => string = k => k): string {
  const lines: string[] = []
  let cur: string | null = null
  for (const s of segs) {
    const t = s.t.trim()
    if (!t) continue
    const who = s.speaker || ''
    if (who !== cur || !lines.length) { lines.push(`[${clock(s.s)}] ${who ? label(who) + ': ' : ''}${t}`); cur = who }
    else lines[lines.length - 1] += ' ' + t
  }
  return lines.join('\n')
}

export function parsePart(v: string | null, total: number): Range | null {
  const m = v?.match(/^(\d+(?:\.\d+)?)-(\d+(?:\.\d+)?)$/)
  if (!m) return null
  const from = Math.round(+m[1] * 1000), to = Math.min(Math.round(+m[2] * 1000), total || Infinity)
  return to - from >= 500 ? { from, to } : null
}

export const partParam = (r: Range) => `${Math.round(r.from / 100) / 10}-${Math.round(r.to / 100) / 10}`

/** "6 min 15 s", "45 s", "1 h 02 min". */
export function length(ms: number): string {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s} s`
  if (s < 3600) return `${Math.floor(s / 60)} min${s % 60 ? ` ${s % 60} s` : ''}`
  return `${Math.floor(s / 3600)} h ${String(Math.floor(s % 3600 / 60)).padStart(2, '0')} min`
}
