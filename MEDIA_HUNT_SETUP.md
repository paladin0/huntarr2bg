# Media Hunt (development integration)

This adds a separate persistent search queue **inside Huntarr 2**, without altering its existing Sonarr/Radarr scheduler. All new hunts are off by default.

## Environment variables

- `MEDIA_HUNT_TOKEN`: long random shared secret. The same value is `CATALOGUE_HUNTARR_TOKEN` in Vanguarr.
- `MEDIA_HUNT_ENABLED=true`: enable background runs. By default manual requests to `POST /api/media-hunt/run` can be used for testing.
- `MEDIA_HUNT_STATE_FILE=/config/media-hunt.json`: persistent state, on the existing /config mount.
- `MEDIA_HUNT_INTERVAL_MINUTES=60`: scheduler wake-up (15..1440).
- `MEDIA_HUNT_BATCH=10`: max requests per cycle (1..100).
- `MEDIA_HUNT_RETRY_MINUTES=360`: delay after a failed search (15..10080).
- `MEDIA_HUNT_RESCAN_HOURS=168`: delay before another search after success (24..8760).
- `MEDIA_HUNT_GAME_AUTO_DISPATCH=false`: search only, no automatic release dispatch. Only set true after testing and verifying download selection.
- `CATALOGUE_LAZYLIBRARIAN_URL`, `CATALOGUE_LAZYLIBRARIAN_KEY`
- `CATALOGUE_RETROARR_URL`, `CATALOGUE_RETROARR_KEY`

## API

Authenticated by `X-Media-Hunt-Token` header:

- `POST /api/media-hunt`: `{"kind":"book", "external_id":"123", "formats":["eBook","AudioBook"]}` or `{"kind":"game", "external_id":"123", "platform_id":19, "game_id":42}`. Game must already exist in RetroArr.
- `GET /api/media-hunt`: states, errors, cooldown dates.
- `POST /api/media-hunt/run`: one bounded cycle; useful in test deployment.

Book requests are first marked Wanted by Vanguarr, then enqueued in Huntarr. Huntarr triggers one LazyLibrarian search per format per batch, letting LazyLibrarian search its configured providers and handle post-processing. RetroArr searches are per registered game. A successful search **is not evidence of an actual download**, so requests are revisited after a cooldown.

### Safety / testing

Do not point a development deployment to the original live databases. Reuse /config only when upgrading via a tested image and with a consistent database backup. Huntarr's native Sonarr/Radarr settings are unchanged; this new media worker is opt-in. `/api/media-hunt` uses a separate token even if Huntarr Basic Auth is disabled.

The existing upstream mainline uses Go 1.27. Build and test with Go >= 1.27 (`go test ./...`, `go build ./...`). This sandbox only has Go 1.23, so Go compilation has **not** been verified here.
