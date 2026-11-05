export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'
export type AppSlug = 'notes' | 'tasks' | 'calendar' | 'money'

type ConnFields = { [K in AppSlug as `${K}_mode`]: ConnMode } & { [K in AppSlug as `${K}_via`]: ConnVia } & { [K in AppSlug as `${K}_detail` | `${K}_url` | `${K}_token` | `${K}_public_url` | `${K}_open_url`]: string } & { [K in AppSlug as `${K}_configured`]: boolean }

export type Settings = ConnFields & {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string; week_start: number; currency: string
  speech_model: string; dictation_model: string; engine: 'local' | 'api'; language: string; vocabulary: string; latin: boolean; speech_api_model: string
  keep_audio_days: number; playback_speed: string
  ai_tidy: boolean; file_on_send: boolean
  notes_folder: string; notes_listen: boolean; tasks_list_id: number; calendar_id: number; money_account_id: number; money_category_id: number
  notify_done: boolean; notify_failed: boolean; notify_inbox: boolean; notify_hour: number
  notify_url: string; notify_format: string; notify_configured: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string; demo: boolean
}

export type MemoStatus = 'queued' | 'working' | 'done' | 'failed'
export type Kind = 'note' | 'todo' | 'idea' | 'journal' | 'meeting' | 'shopping' | 'reminder'

export interface Segment { s: number; e: number; t: string }

export interface Action {
  type: 'task' | 'event' | 'journal' | 'expense'
  title: string; notes?: string; due?: string; start?: string; end?: string; location?: string; text?: string
  amount?: number; currency?: string; date?: string
  sent_to?: string; sent_ref?: string; sent_url?: string; skipped?: boolean
}

export interface Sent { id: number; memo_id: number; app: string; kind: string; ref: string; title: string; url: string; created_at: string }

export interface Progress { stage: 'preparing' | 'model' | 'writing'; pct: number }

export interface Memo {
  id: number; title: string; title_set: boolean; recorded_at: string; duration_ms: number; audio_type: string; audio_bytes: number; has_audio: boolean
  peaks: number[]; language: string; status: MemoStatus; error: string; transcript: string; segments: Segment[]; edited: boolean; engine: string
  summary: string; tidy: string; kind: Kind | ''; kind_set: boolean; actions: Action[]; ai_status: '' | 'working' | 'done' | 'failed'; ai_error: string; ai_provider: string
  tags: string; starred: boolean; filed: boolean; source: string; source_ref: string; audio_gone_at: string; created_at: string; updated_at: string
  sent: Sent[]; url: string; audio_url: string; progress?: Progress; queue?: number
}

export interface Counts { all: number; inbox: number; starred: number; working: number; failed: number; minutes: number }
export interface MemoPage { items: Memo[]; total: number; counts: Counts }

export interface SpeechModel { id: string; name: string; engine: 'whisper' | 'parakeet'; file: string; bytes: number; note: string; ready: boolean; current: boolean; dictation: boolean; runs: boolean; download?: { got: number; total: number; error: string; done: boolean } }
export interface Speech {
  engine: 'local' | 'api'; ready: boolean; detail: string; whisper: boolean; ffmpeg: boolean; model: string; dictation_model: string; models: SpeechModel[]; threads: number
  languages: { code: string; name: string }[]; api_model: string; demo: boolean
}

export interface Picked<T> { connected: boolean; detail?: string; items: T[] }
export interface MoneyAccount { id: number; name: string; kind: string; currency: string; everyday: boolean }
export interface MoneyCategory { id: number; name: string; full_name: string }
export interface CalendarItem { id: number; name: string; color: string; writable: boolean; is_default: boolean }
export interface NoteFolder { path: string; count: number }
export interface TaskList { id: number; name: string; color: string }
export interface TagCount { tag: string; count: number }
