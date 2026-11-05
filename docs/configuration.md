# Configuration

Environment variables; every one has a default. Personal settings (language, words to expect, notifications, connections, AI) live in the UI per person — the `OPENAI_*`/`NOTIFY_*` values below are copied once into the first admin's settings.

| Variable | Default | What |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL (links in pushes and notes, the OIDC callback) |
| `DATA_DIR` | `./data` | Database, recordings, speech models, Core token, backups |
| `DB_PATH` | `$DATA_DIR/pocket-voice.db` | SQLite file |
| `AUDIO_DIR` | `$DATA_DIR/audio` | Recordings, a folder per person |
| `MAX_AUDIO_BYTES` | `536870912` | Largest recording upload (512 MB) |
| `MAX_BODY_BYTES` | `67108864` | Largest JSON request, e.g. an export being imported (64 MB) |
| `WHISPER_BIN`, `FFMPEG_BIN` | `whisper-cli`, `ffmpeg` | Both are in the Docker image (whisper.cpp on amd64 and arm64) |
| `WHISPER_MODEL` | `large-v3-turbo-q5_0` | The box's model until an admin picks one in Settings → Writing out: `base`, `small`, `large-v3-turbo-q5_0` |
| `WHISPER_THREADS` | half the cores, 1–8 | Threads whisper uses |
| `MODELS_DIR`, `MODEL_BASE_URL` | `$DATA_DIR/models`, Hugging Face `ggerganov/whisper.cpp` | Where models are kept and downloaded from (a mirror works) |
| `TZ` | – | Time zone for "tomorrow" in memos and the morning message |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | – | Seed the first admin while the database is empty |
| `SESSION_TTL`, `SECURE_COOKIES` | `720h`, `false` | Set `SECURE_COOKIES=true` behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL`, `NOTIFY_FORMAT` | – | Apprise (`…/notify/key`), ntfy topic URL or JSON webhook; format `apprise`/`ntfy`/`json` |
| `NOTIFY_HOUR` | `8` | Default hour of the morning message (each person can change theirs) |
| `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_BASE_URL` | – | Tidying up without Pocket AI; with "OpenAI or a speech server" under Writing out, recordings go to this API too |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY`, `METRICS_ENABLED`, `LOG_LEVEL`, `LOG_FORMAT` | `true`, `true`, `info`, `text` | |
| `DEMO_DATA` | `false` | Seed three months of memos (lists, meetings, ideas, journal bits in English and Bosnian, with what was sent where) into the owner account; speech models are not downloaded |

Commands: `pocket-voice backup [-o FILE]`, `pocket-voice reset-password [-email] [-password]`, `pocket-voice version`.

Connections to Pocket Notes, Tasks, Calendar and Money are made per person under Settings → Connected apps: through Pocket Core (nothing to paste) or directly with the app's URL and an API token.
