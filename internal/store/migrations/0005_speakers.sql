-- Who spoke in a meeting. The call side is split into its voices; each voice
-- of a memo is a speaker (S1, S2, … next to You) with a name once known.
-- speaker_voices keeps each speaker's voice print (an embedding) so a named
-- voice can be recognised in the next meeting; it never leaves this box.
ALTER TABLE memos ADD COLUMN speakers TEXT NOT NULL DEFAULT '[]';
ALTER TABLE memos ADD COLUMN speaker_voices TEXT NOT NULL DEFAULT '{}';
ALTER TABLE memos ADD COLUMN speakers_status TEXT NOT NULL DEFAULT '';  -- '' | working | done | failed
ALTER TABLE memos ADD COLUMN speakers_error TEXT NOT NULL DEFAULT '';

-- The people whose voices the person has named: a voice print learned from
-- each meeting they were named or recognised in (at most 30, newest kept).
CREATE TABLE people (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  name_key   TEXT NOT NULL,
  voices     TEXT NOT NULL DEFAULT '[]',   -- [{emb, ms, memo_id, at, auto}]
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, name_key)
);
