# Backup

The words live in one SQLite file, `DATA_DIR/pocket-voice.db` (WAL mode); the recordings are files in `DATA_DIR/audio/<person>/`. Take a consistent copy of the database with `pocket-voice backup` (writes `DATA_DIR/backups/pocket-voice-<time>.db` with `VACUUM INTO`) or Settings → Backup & data → Download database (admin), and back up `audio/` as it is. Back up that copy, not the live file. `DATA_DIR/models/` holds the downloaded speech models (they download again; no need to back them up), `DATA_DIR/tmp/` is scratch. `DATA_DIR/core.token*` is the Pocket Core registration.

Restore: stop the app, replace `pocket-voice.db` (remove `-wal`/`-shm` files) and `audio/`, start.

Every person can also export their own memos: JSON (`/api/v1/export.json`, re-importable) or every recording with its words in a ZIP (`/api/v1/export.zip`).
