-- Folders: the person's own containers for memos and meetings ("Project X",
-- "1:1 with Heidi"). A memo is in at most one folder (0 = none); removing a
-- folder keeps its memos, they just are in no folder any more.
CREATE TABLE folders (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  name_key   TEXT NOT NULL,                        -- lower case, single spaces: one folder per name
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX folders_name ON folders(user_id, name_key);

ALTER TABLE memos ADD COLUMN folder_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX memos_folder ON memos(user_id, folder_id, recorded_at);
