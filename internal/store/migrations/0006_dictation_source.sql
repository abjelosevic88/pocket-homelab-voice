-- Where a dictation was made: the client that recorded it ("Pocket Launcher" on
-- the Mac, "Pocket AI app" on the phone, "Pocket AI on the web"), next to
-- device (which machine) and app (where the words went). Every dictation
-- before this came from Pocket Launcher.
ALTER TABLE dictations ADD COLUMN source TEXT NOT NULL DEFAULT '';
UPDATE dictations SET source = 'Pocket Launcher' WHERE source = '';
