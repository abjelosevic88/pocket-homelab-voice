-- Dictation history: every text Pocket Launcher (or another client) typed
-- into an app for the person by voice. Only the words and where they went:
-- the recording never comes here. `ref` is the client's own id, so a
-- retried POST finds the row it already made.
CREATE TABLE dictations (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ref         TEXT NOT NULL,
  text        TEXT NOT NULL DEFAULT '',             -- what was typed
  raw         TEXT NOT NULL DEFAULT '',             -- the words before AI polishing ('' = not polished)
  app         TEXT NOT NULL DEFAULT '',             -- display name of the app it went into
  app_id      TEXT NOT NULL DEFAULT '',             -- macOS bundle id
  duration_ms INTEGER NOT NULL DEFAULT 0,           -- speaking time
  words       INTEGER NOT NULL DEFAULT 0,
  engine      TEXT NOT NULL DEFAULT '',
  language    TEXT NOT NULL DEFAULT '',
  device      TEXT NOT NULL DEFAULT '',
  polished    INTEGER NOT NULL DEFAULT 0,
  search      TEXT NOT NULL DEFAULT '',             -- folded text for search
  dictated_at TEXT NOT NULL,                        -- RFC 3339, UTC, fixed width (milliseconds)
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, ref)
);
CREATE INDEX dictations_user ON dictations(user_id, dictated_at);
CREATE INDEX dictations_app ON dictations(user_id, app);
