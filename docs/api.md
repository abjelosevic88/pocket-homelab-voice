# API

Everything the UI does goes through `/api/v1`, JSON in and out. Sign in with the session cookie, or send a personal token (Settings → API & shortcuts) as `Authorization: Bearer pvo_…`. Each person sees only their own memos: another person's id answers 404.

## Memos

| Call | What |
|---|---|
| `GET /memos?q&kind&tag&inbox=1&starred=1&status&from&to&limit&offset&oldest=1` | `{items, total, counts: {all, inbox, starred, working, failed, minutes}}`, newest first. `q` searches title and words, accents and alphabet ignored; `from`/`to` are local days |
| `POST /memos` | multipart: `file` (any recording), optional `title`, `kind`, `tags`, `recorded_at` (RFC 3339), `duration_ms`, `language`, `from` (`upload`) — queued to be written out. Or JSON `{text, title, kind, tags, recorded_at}` for a typed memo. From a linked app also `source_ref` (sending the same one twice returns the first) |
| `GET /memos/{id}` | The memo: `status` (queued, working, done, failed) with `progress` `{stage, pct}` and `queue` while it waits, `transcript`, `segments` `[{s, e, t}]` (ms), `tidy`, `summary`, `kind`, `tags`, `actions`, `sent`, `audio_url` |
| `PUT /memos/{id}` | Any of `title`, `transcript`, `tidy`, `summary`, `kind`, `tags`, `starred`, `filed`, `recorded_at` |
| `DELETE /memos/{id}` | The memo and its recording; what was sent to other apps stays there |
| `GET /memos/{id}/audio` | The recording (range requests work); `?download=1` to save it |
| `POST /memos/{id}/retry {language}` | Write it out again |
| `POST /memos/{id}/tidy` | Tidy it up with AI now |
| `POST /memos/{id}/actions/{i} {skipped}` | Skip a suggestion, or bring it back |
| `POST /memos/{id}/send` | `{to: note\|daily\|task\|event\|expense, action, title, text, folder, due, notes, list_id, start, end, location, calendar_id, amount, currency, date, account_id}` → `{sent, memo}`; `action` marks that suggestion as sent |

`actions` are what the AI found: `{type: task, title, due, notes}`, `{type: event, title, start, end, location}` (`start` is `YYYY-MM-DD` or `YYYY-MM-DDTHH:MM`), `{type: journal, text}`, `{type: expense, title, amount, currency, date}` (amount in major units); once sent they carry `sent_to`, `sent_ref`, `sent_url`.

## For dashboards, the journal and other apps

| Call | What |
|---|---|
| `GET /summary` | `{memos, inbox, starred, working, failed, minutes, today, latest_inbox, url}` |
| `GET /day/{YYYY-MM-DD}` | `{memos: [{id, title, kind, time, duration_ms, summary, text, status, sent, url}], count, duration_ms}` — what Pocket Day writes into the journal |
| `GET /search?q&limit` | `{items, total}` |
| `GET /tags`, `GET /kinds` | Tags with counts; kinds and how many of each |
| `POST /transcribe` | multipart `file`, optional `language` → `{text, language, segments, engine}`. Written out with the person's engine, language and words; nothing is kept |
| `GET /speech` | How memos are written out: engine, ready, the model and every model with its download state |

## Links (Pocket Core)

Voice offers `voice:read` (memos, a memo and its recording, summary, day, search, tags, kinds), `voice:write` (`POST /memos`, and `PUT/DELETE` of the memos that app added) and `voice:transcribe` (`POST /transcribe`, `GET /speech`). Everything else answers 403 to a link token.

It uses Pocket Notes (`notes:write`, `notes:read`), Pocket Tasks (`tasks:write`, `tasks:read`), Pocket Calendar (`calendar:write`, `calendar:read`), Pocket Money (`transactions:write`, `accounts:read`) and Pocket AI inference. Events, each with `user_email` and subject `memo:<id>`: `voice.memo_recorded`, `voice.memo_transcribed` (also `text`, `engine`, `language`) and `voice.memo_filed` (`app`, `kind`, `ref`, `item_title`, `item_url`).

## The rest

`GET/PUT /settings`, `GET /settings/system`, `POST /speech/models/{id}` and `DELETE /speech/models/{id}` (admin: download or remove a model), `GET /links/notes/folders`, `/links/tasks/lists`, `/links/calendar/calendars`, `/links/money/accounts`, `/links/money/categories`, `GET /ai/status`, `GET /ai/models`, `POST /integrations/test {which}`, `POST /notify/test`, `POST /notify/check` (send the morning message now), `GET/POST/DELETE /tokens`, `GET /backup` (admin), `GET /export.json`, `GET /export.zip`, `POST /import` (a Pocket Voice export). Health: `GET /healthz`, `/readyz`, `/metrics`.
