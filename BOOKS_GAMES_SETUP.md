# Read-only LazyLibrarian and RetroArr catalogue adapters

This patch adds `/api/catalogue/authors`, `/api/catalogue/books`, and `/api/catalogue/games` to the existing authenticated Huntarr 2 HTTP server. These are read-only JSON pass-throughs for diagnostic/library-preview use. Existing ARR instance models, search scheduler, encryption key and database are unchanged. There is **no** automatic hunting of books or games yet.

Configure container environment:

- `CATALOGUE_LAZYLIBRARIAN_URL=http://192.168.4.40:31089`
- `CATALOGUE_LAZYLIBRARIAN_KEY=<secret>`
- `CATALOGUE_RETROARR_URL=http://192.168.4.40:30073`
- `CATALOGUE_RETROARR_KEY=<secret>`

Use appropriate credentials and keep Huntarr authentication enabled. Upstream addresses are configured by the administrator rather than accepted from users. Requests disable HTTP redirects and limit response size to 8 MiB. Some large LazyLibrarian catalogues may exceed that limit. To preserve compatibility with the existing scheduler, do **not** add LazyLibrarian or RetroArr to the ordinary ARR adapter types until their different search and quality semantics have been implemented and tested.

For live testing, build a fork image in a separate container with a **copied** Huntarr config volume and a distinct published port. Preserve `encryption.key`. Never run two containers against the same SQLite WAL database.
