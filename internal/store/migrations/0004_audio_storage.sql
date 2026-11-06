-- Where a recording is kept once written out: '' = this box (DATA_DIR/audio),
-- else the key of the person's chosen place (Settings → Where recordings are
-- kept), and audio_away its path there. Only the mover writes these, so an
-- edit saved meanwhile cannot undo a move.
ALTER TABLE memos ADD COLUMN audio_at TEXT NOT NULL DEFAULT '';
ALTER TABLE memos ADD COLUMN audio_away TEXT NOT NULL DEFAULT '';
