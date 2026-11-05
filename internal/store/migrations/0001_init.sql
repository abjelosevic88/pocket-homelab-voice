CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag, the speech model); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);

-- ---------- Pocket Voice: everything below is per person ----------

-- A voice memo: the recording (a file under DATA_DIR/audio/<user>/), its
-- words with their timings, what the AI made of it and where it went.
CREATE TABLE memos (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title         TEXT NOT NULL DEFAULT '',
  title_set     INTEGER NOT NULL DEFAULT 0,           -- named by the person: the AI leaves it
  recorded_at   TEXT NOT NULL,                        -- RFC 3339, UTC
  duration_ms   INTEGER NOT NULL DEFAULT 0,
  audio         TEXT NOT NULL DEFAULT '',             -- file name; '' = no recording (typed, or let go)
  audio_type    TEXT NOT NULL DEFAULT '',             -- MIME type of the file
  audio_bytes   INTEGER NOT NULL DEFAULT 0,
  peaks         TEXT NOT NULL DEFAULT '[]',           -- loudness bars 0-100 for the waveform
  language      TEXT NOT NULL DEFAULT '',             -- asked for ('' = find out), then what was heard
  status        TEXT NOT NULL DEFAULT 'queued',       -- queued | working | done | failed
  error         TEXT NOT NULL DEFAULT '',
  transcript    TEXT NOT NULL DEFAULT '',             -- the words (the person may correct them)
  segments      TEXT NOT NULL DEFAULT '[]',           -- [{s, e, t}] in ms
  edited        INTEGER NOT NULL DEFAULT 0,
  engine        TEXT NOT NULL DEFAULT '',             -- "whisper large-v3-turbo-q5_0", "api gpt-4o-mini-transcribe", "typed"
  summary       TEXT NOT NULL DEFAULT '',
  tidy          TEXT NOT NULL DEFAULT '',             -- the words cleaned up by the AI
  kind          TEXT NOT NULL DEFAULT '',             -- note | todo | idea | journal | meeting | shopping | reminder
  kind_set      INTEGER NOT NULL DEFAULT 0,
  actions       TEXT NOT NULL DEFAULT '[]',           -- what the AI found to send elsewhere
  ai_status     TEXT NOT NULL DEFAULT '',             -- '' | working | done | failed
  ai_error      TEXT NOT NULL DEFAULT '',
  ai_provider   TEXT NOT NULL DEFAULT '',
  tags          TEXT NOT NULL DEFAULT '',
  starred       INTEGER NOT NULL DEFAULT 0,
  filed         INTEGER NOT NULL DEFAULT 0,           -- dealt with: out of the inbox
  search        TEXT NOT NULL DEFAULT '',             -- folded title + words for search
  source        TEXT NOT NULL DEFAULT '',             -- '' recorded | upload | import | <app slug>
  source_ref    TEXT NOT NULL DEFAULT '',
  audio_gone_at TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX memos_user ON memos(user_id, recorded_at);
CREATE INDEX memos_inbox ON memos(user_id, filed, recorded_at);
CREATE INDEX memos_queue ON memos(status, created_at);
CREATE UNIQUE INDEX memos_source ON memos(user_id, source, source_ref) WHERE source_ref <> '';

-- Where a memo went: a note or a line in today's note (Pocket Notes), tasks
-- (Pocket Tasks), an event (Pocket Calendar), an expense (Pocket Money).
CREATE TABLE sent (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  memo_id    INTEGER NOT NULL REFERENCES memos(id) ON DELETE CASCADE,
  app        TEXT NOT NULL,
  kind       TEXT NOT NULL,
  ref        TEXT NOT NULL DEFAULT '',
  title      TEXT NOT NULL DEFAULT '',
  url        TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX sent_memo ON sent(user_id, memo_id);
CREATE INDEX sent_day ON sent(user_id, created_at);
