-- The person's dictionary: words to expect (names, how they are spelled) and
-- replacements ("pocket ai" → "Pocket AI"), used when writing out and when
-- polishing dictation. phrase_key is the phrase in lower case (Unicode), so
-- one phrase is there once whatever its capitals.
CREATE TABLE dictionary (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  phrase       TEXT NOT NULL,
  phrase_key   TEXT NOT NULL,
  replace_with TEXT NOT NULL DEFAULT '',            -- '' = a word to expect
  source       TEXT NOT NULL DEFAULT 'manual',      -- manual | learned
  uses         INTEGER NOT NULL DEFAULT 0,          -- how often the replacement fired
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, phrase_key)
);

-- How dictation is polished: a default style and one per app.
CREATE TABLE dictation_styles (
  user_id       INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  default_style TEXT NOT NULL DEFAULT 'natural',
  apps          TEXT NOT NULL DEFAULT '[]'           -- [{app_id, app, style}]
);

-- A meeting: a stereo recording, the person on the left, the call on the
-- right, written out per speaker.
ALTER TABLE memos ADD COLUMN meeting INTEGER NOT NULL DEFAULT 0;
