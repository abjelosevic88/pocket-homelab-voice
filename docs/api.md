# API

Everything the UI does goes through `/api/v1`, JSON in and out. Sign in with the session cookie, or send a personal token (Settings → API & shortcuts) as `Authorization: Bearer pvo_…`. Each person sees only their own memos: another person's id answers 404.

## Memos

| Call | What |
|---|---|
| `GET /memos?q&kind&tag&inbox=1&starred=1&status&from&to&limit&offset&oldest=1` | `{items, total, counts: {all, inbox, starred, working, failed, minutes}}`, newest first. `q` searches title and words, accents and alphabet ignored; `from`/`to` are local days |
| `POST /memos` | multipart: `file` (any recording), optional `title`, `kind`, `tags`, `recorded_at` (RFC 3339), `duration_ms`, `language`, `from` (`upload`), `meeting=1` (see Meetings) — queued to be written out. Or JSON `{text, title, kind, tags, recorded_at}` for a typed memo. From a linked app also `source_ref` (sending the same one twice returns the first) |
| `GET /memos/{id}` | The memo: `status` (queued, working, done, failed) with `progress` `{stage, pct}` and `queue` while it waits, `transcript`, `segments` `[{s, e, t}]` (ms), `tidy`, `summary`, `kind`, `tags`, `actions`, `sent`, `audio_url` |
| `PUT /memos/{id}` | Any of `title`, `transcript`, `tidy`, `summary`, `kind`, `tags`, `starred`, `filed`, `recorded_at` |
| `DELETE /memos/{id}` | The memo and its recording; what was sent to other apps stays there |
| `GET /memos/{id}/audio` | The recording (range requests work); `?download=1` to save it |
| `POST /memos/{id}/retry {language}` | Write it out again |
| `POST /memos/{id}/tidy` | Tidy it up with AI now |
| `POST /memos/{id}/actions/{i} {skipped}` | Skip a suggestion, or bring it back |
| `POST /memos/{id}/send` | `{to: note\|daily\|task\|event\|expense, action, title, text, folder, due, notes, list_id, start, end, location, calendar_id, amount, currency, date, account_id}` → `{sent, memo}`; `action` marks that suggestion as sent |

`actions` are what the AI found: `{type: task, title, due, notes}`, `{type: event, title, start, end, location}` (`start` is `YYYY-MM-DD` or `YYYY-MM-DDTHH:MM`), `{type: journal, text}`, `{type: expense, title, amount, currency, date}` (amount in major units); once sent they carry `sent_to`, `sent_ref`, `sent_url`.

## Meetings

`POST /memos` with `meeting=1` takes a **stereo** recording of a call: the left channel is the person's microphone ("You"), the right the computer's sound, the others on the call ("Others"). The memo gets `meeting: true` and kind `meeting`; each channel is written out on its own with its quiet stretches silenced, and the two are merged by time. Every segment then carries `speaker` (`"You"` or `"Others"`; segments of other memos have no `speaker`), and the transcript is lines `You: …` / `Others: …`. A channel that is silent the whole time is left out (a one-speaker meeting); a recording that is not stereo is written out as one voice. The AI's tidy-up of a meeting keeps the speaker labels, writes a summary with the decisions (`Decisions:` lines at the end of `summary`) and puts the action items in `actions`. The playable copy keeps both channels.

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

## Dictionary

Words Voice should expect and replacements, per person. An entry with an empty `replace_with` is a **word to expect** (a name, how a word is spelled); with `replace_with` it is a **replacement**: wherever the phrase is heard as whole words, in any capitals, it is written as `replace_with` (its own capitals kept).

| Call | What |
|---|---|
| `GET /dictionary` | `{items: [{id, phrase, replace_with, source, uses, created_at}]}` by phrase. `source` is `manual` or `learned`; `uses` counts how often the replacement was made |
| `POST /dictionary` | `{phrase, replace_with?, source?}` → **201** `{item}`. The same phrase again (any capitals) → **200** with the row that is there, its `replace_with` changed when one was sent. `phrase` 1–100 characters, `replace_with` at most 200, `source` `manual` (default) or `learned` |
| `PUT /dictionary/{id}` | `{phrase?, replace_with?}` → `{item}`; a phrase another entry has answers 409 |
| `DELETE /dictionary/{id}` | 204 |

Where it is used: its words (the replacement's spelling when there is one) go into the speech model's prompt after the "Names and words to expect" setting, for memos and `POST /transcribe` (Whisper uses it; Parakeet has no prompt); the replacements are made in what was written out (memo transcripts and their segments, the `POST /transcribe` answer) and in `POST /polish` and `POST /rewrite`; the AI is told the spellings when it tidies, polishes and rewrites.

## Dictation styles, polishing and rewriting

| Call | What |
|---|---|
| `GET /dictation/styles` | `{default: "natural", apps: [{app_id, app, style}]}` |
| `PUT /dictation/styles` | The same shape; replaces the whole list. Each app needs an `app_id` (bundle id) or an `app` name; the first entry for an app counts. Answers the stored styles |
| `POST /polish` | `{text, language?, style?, app?, app_id?}` → `{text, provider, style}`: dictated text tidied by the person's AI. Without `style`, the style set for the app (by `app_id`, then by `app` name) or the default. Filler words and false starts out, self-corrections resolved ("at 5 pm, actually 6 pm" → "at 6 pm"), the dictionary applied; the AI is told which app it goes into. AI not connected: **422** `{error}`; nothing is kept |
| `POST /rewrite` | `{text, instruction, language?, app?, style?}` → `{text}`: the selected `text` rewritten by the spoken `instruction` ("make it shorter", "translate to English", "reply saying yes but next week"); with empty `text`, new text written from the instruction. 30 s; AI not connected: **409** `{error}`; nothing is kept |

Styles: `natural` (punctuation, capitals, fillers out — what polishing always did), `casual` (relaxed, lower case fine, no sign-offs: chat), `formal` (complete sentences, proper punctuation: email and documents), `code` (identifiers, paths and commands kept exactly, backticks around code-like words, no full stop at the end), `none` (no AI: `POST /polish` answers the text with only the dictionary's replacements made, `provider` empty, even when no AI is connected).

## For dashboards, the journal and other apps

| Call | What |
|---|---|
| `GET /summary` | `{memos, inbox, starred, working, failed, minutes, today, latest_inbox, url, dictation_words_today, dictation_sessions_today}` |
| `GET /day/{YYYY-MM-DD}` | `{memos: [{id, title, kind, time, duration_ms, summary, text, status, sent, url}], count, duration_ms}` — what Pocket Day writes into the journal |
| `GET /search?q&limit` | `{items, total, dictations: {items, total}}`: the memos, and the dictations with these words |
| `GET /tags`, `GET /kinds` | Tags with counts; kinds and how many of each |
| `POST /transcribe` | multipart `file`, optional `language` → `{text, language, segments, engine}`. Written out with the person's engine, language and words; nothing is kept |
| `GET /speech` | How memos are written out: engine, ready, the memo and dictation models and every model with its engine and download state |

## Links (Pocket Core)

Voice offers `voice:read` (memos, a memo and its recording, summary, day, search, tags, kinds, the dictation history: `GET /dictations`, `GET /dictations/stats`; the dictionary and styles: `GET /dictionary`, `GET /dictation/styles`), `voice:write` (`POST /memos`, and `PUT/DELETE` of the memos that app added) and `voice:transcribe` (`POST /transcribe`, `POST /polish`, `POST /rewrite`, `GET /speech`). Everything else answers 403 to a link token, including every change to the dictionary, the styles and the dictation history.

It uses Pocket Notes (`notes:write`, `notes:read`), Pocket Tasks (`tasks:write`, `tasks:read`), Pocket Calendar (`calendar:write`, `calendar:read`), Pocket Money (`transactions:write`, `accounts:read`) and Pocket AI inference. Events, each with `user_email` and subject `memo:<id>`: `voice.memo_recorded`, `voice.memo_transcribed` (also `text`, `engine`, `language`) and `voice.memo_filed` (`app`, `kind`, `ref`, `item_title`, `item_url`).

## The rest

`GET/PUT /settings`, `GET /settings/system`, `POST /speech/models/{id}` and `DELETE /speech/models/{id}` (admin: download or remove a model), `GET /links/notes/folders`, `/links/tasks/lists`, `/links/calendar/calendars`, `/links/money/accounts`, `/links/money/categories`, `GET /ai/status`, `GET /ai/models`, `POST /integrations/test {which}`, `POST /notify/test`, `POST /notify/check` (send the morning message now), `GET/POST/DELETE /tokens`, `GET /backup` (admin), `GET /export.json` (memos, where they went, the dictation history, the dictionary and the styles), `GET /export.zip`, `POST /import` (a Pocket Voice export). Health: `GET /healthz`, `/readyz`, `/metrics`.
