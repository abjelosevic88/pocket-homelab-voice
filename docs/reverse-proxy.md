# Reverse proxy

Set `BASE_URL` to the public address and `SECURE_COOKIES=true` behind HTTPS — browsers only let a page use the microphone over HTTPS. See [deploy/reverse-proxy/Caddyfile](../deploy/reverse-proxy/Caddyfile). Recordings are uploaded in one request: if your proxy limits request bodies, allow at least `MAX_AUDIO_BYTES` (512 MB by default). `/api/*` is reachable for Homepage widgets, Shortcuts and linked apps; they send their own bearer token.
