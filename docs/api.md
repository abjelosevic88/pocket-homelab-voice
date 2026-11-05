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

## Dictation

The history of what Pocket Launcher's dictation on the Mac typed for you (hold a key, talk, the words go into whatever text box has focus). The client posts each finished dictation from a retrying outbox. Session cookie, a personal token and Pocket Core device tokens (`pcd_`) all work; each person sees only their own (another person's id answers 404).

| Call | What |
|---|---|
| `POST /dictations` | JSON `{ref, text, raw, app, app_id, duration_ms, engine, language, device, polished, dictated_at}` → `{dictation}`, **201** when new. `ref` (1–64 characters, the client's own id, e.g. a UUID) makes it idempotent per person: posting the same `ref` again answers **200** with the row as it was first stored. `text` (required, trimmed, at most 20000 characters) is what was typed; `raw` the words before AI polishing (`""` when not polished); `app` the app's name ("Slack"), `app_id` its bundle id; `duration_ms` (≥ 0) the speaking time; `engine` ("parakeet-v3", "whisper-large-v3-turbo", "voice"), `language`, `device`, `polished`; `dictated_at` RFC 3339 with an offset (default now; more than 5 minutes in the future answers 400). The server counts the `words` |
| `GET /dictations?q&app&from&to&before&limit` | `{items, total, next}`, newest first. `q` finds the words in `text`, case and accents ignored ("cevapi" finds "Ćevapi"); `app` is one app exactly; `from`/`to` are local days (`YYYY-MM-DD`, server time zone); `limit` 50 by default, at most 200; `before` is the cursor: send the `next` of the last page (`0` when there is no more). `total` counts everything that matches the filter |
| `DELETE /dictations/{id}` | 204 |
| `DELETE /dictations` | Clears all of the caller's history → `{deleted}` |
| `GET /dictations/stats` | `{words, sessions, duration_ms, saved_seconds, wpm, today: {words, sessions, saved_seconds}, week: {…}, streak, apps: [{app, sessions, words}], days: [{date, words, sessions}]}` |

A dictation is `{id, ref, text, raw, app, app_id, duration_ms, words, engine, language, device, polished, dictated_at, created_at}`; times come back in UTC (`2026-11-05T14:28:12.000Z`).

In the stats, `saved_seconds` is the time saved over typing at 40 words a minute: the sum over dictations of `max(0, words / 40 × 60 − duration_ms / 1000)`, rounded to whole seconds. `wpm` is the speaking speed, `words / (duration_ms / 60000)` rounded (0 without any duration). `today` and `week` (the last seven days, today included) use the server's time zone; `streak` is the number of days in a row with at least one dictation up to today, or up to yesterday while today has none yet; `apps` are the top eight by sessions; `days` are the last 30 days, oldest first, with the empty ones as zeros. Every number is a whole number.

## For dashboards, the journal and other apps

| Call | What |
|---|---|
| `GET /summary` | `{memos, inbox, starred, working, failed, minutes, today, latest_inbox, url, dictation_words_today, dictation_sessions_today}` |
| `GET /day/{YYYY-MM-DD}` | `{memos: [{id, title, kind, time, duration_ms, summary, text, status, sent, url}], count, duration_ms}` — what Pocket Day writes into the journal |
| `GET /search?q&limit` | `{items, total}` |
| `GET /tags`, `GET /kinds` | Tags with counts; kinds and how many of each |
| `POST /transcribe` | multipart `file`, optional `language` → `{text, language, segments, engine}`. Written out with the person's engine, language and words; nothing is kept |
| `POST /polish` | JSON `{text, language}` → `{text, provider}`: dictated text tidied by the person's AI (filler words out, punctuation in, nothing added); nothing is kept |
| `GET /speech` | How memos are written out: engine, ready, the memo and dictation models and every model with its engine and download state |

## Links (Pocket Core)

Voice offers `voice:read` (memos, a memo and its recording, summary, day, search, tags, kinds, and the dictation history: `GET /dictations`, `GET /dictations/stats`), `voice:write` (`POST /memos`, and `PUT/DELETE` of the memos that app added) and `voice:transcribe` (`POST /transcribe`, `POST /polish`, `GET /speech`). Everything else answers 403 to a link token.

It uses Pocket Notes (`notes:write`, `notes:read`), Pocket Tasks (`tasks:write`, `tasks:read`), Pocket Calendar (`calendar:write`, `calendar:read`), Pocket Money (`transactions:write`, `accounts:read`) and Pocket AI inference. Events, each with `user_email` and subject `memo:<id>`: `voice.memo_recorded`, `voice.memo_transcribed` (also `text`, `engine`, `language`) and `voice.memo_filed` (`app`, `kind`, `ref`, `item_title`, `item_url`).

## The rest

`GET/PUT /settings`, `GET /settings/system`, `POST /speech/models/{id}` and `DELETE /speech/models/{id}` (admin: download or remove a model), `GET /links/notes/folders`, `/links/tasks/lists`, `/links/calendar/calendars`, `/links/money/accounts`, `/links/money/categories`, `GET /ai/status`, `GET /ai/models`, `POST /integrations/test {which}`, `POST /notify/test`, `POST /notify/check` (send the morning message now), `GET/POST/DELETE /tokens`, `GET /backup` (admin), `GET /export.json` (memos, where they went and the dictation history), `GET /export.zip`, `POST /import` (a Pocket Voice export). Health: `GET /healthz`, `/readyz`, `/metrics`.
