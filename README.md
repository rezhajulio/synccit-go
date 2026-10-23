# synccit-go

Go + SQLite port of [synccit](https://github.com/drakeapps/synccit) by James
Wilson / drakeapps — it syncs your Reddit history (visited links and read
comment counts) between devices. When you open a link or a comment thread on
one device, the ID and comment count are recorded here; on every other device
the link then shows as read, along with how many comments are new.

This is an API-only port: the original PHP web UI (account pages, FAQ,
device manager pages) is not included. Accounts and device auth codes are
managed with the built-in CLI instead, and **registration is locked by
default** — API account creation is disabled unless you explicitly open it.

License: GPL-3.0-or-later, same as the original. See [LICENSE](LICENSE).

## Quickstart

```sh
# Build (pure Go, no CGO)
CGO_ENABLED=0 go build -o synccit ./cmd/synccit

# Create your account and a device auth code
./synccit user add james --email james@example.com   # prompts for a password
./synccit device add james "my phone"                # prints the 6-char auth code

# Run the server
./synccit serve --addr :8080
```

Then, in your synccit client (browser extension, Android/iOS Reddit client
with synccit support), set:

- **API URL:** `https://your-host/api.php` (the clean alias `/api` works too,
  as do `/account.php` and `/account` for the account API)
- **Username:** your synccit username
- **Auth code:** the device code printed by `device add`

### Docker

```sh
docker compose up -d --build
./synccit user add james   # or: docker compose exec synccit synccit user add james
```

The database lives in `./data/synccit.db` (a volume). Back it up by copying
that one file (WAL mode: stop the container or copy `synccit.db*` together).

## Configuration

Environment variables; `serve` flags override them.

| Variable                   | Flag                  | Default        | Meaning                                   |
| -------------------------- | --------------------- | -------------- | ----------------------------------------- |
| `SYNCCIT_DB`               | `--db`                | `./synccit.db` | SQLite database path                      |
| `SYNCCIT_ADDR`             | `--addr`              | `:8080`        | Listen address                            |
| `SYNCCIT_ALLOW_REGISTRATION` | `--allow-registration` | disabled    | `true`/`1` enables API `mode=create`      |

The CLI subcommands read `SYNCCIT_DB` for the database path.

## CLI reference

```
synccit serve [--db <path>] [--addr <addr>] [--allow-registration]
synccit user add <username> [--email <email>] [--password <pw>]
synccit user list
synccit user passwd <username> [--password <pw>]
synccit user delete <username>          # also deletes devices, links, logins
synccit device add <username> "<device name>"
synccit device list <username>
synccit device revoke <username> <code>
synccit help
```

Without `--password`, `user add` / `user passwd` prompt (hidden input on a
terminal, with confirmation). Account validation matches the original:
username ≥ 3 chars of `[a-zA-Z0-9_]`, password ≥ 6 chars, email optional but
must contain exactly one `@` when given.

## API summary

Wire-compatible with the original synccit API v1. Full original docs live in
the [upstream README](https://github.com/drakeapps/synccit#api-docs).

### Sync API — `POST /api.php` (alias `/api`), `X-API: 1`, `X-Revision: 12`

Requests are a form field `data` containing JSON (`type=json`, default) or
XML (`type=xml`, or a payload starting with `<?xml`), or — for update/read
only — plain form fields. Modes: `update`, `read`, `history` (100 per page,
`offset`/`time`), `create` (gated, see above), `addauth` (password → new
device code). Authentication is `username` + device `auth` code; the account
password is accepted as a fallback, like the original revision 11+.

Example JSON update:

```json
{
  "username": "james",
  "auth": "9m89x0",
  "dev": "synccit json",
  "mode": "update",
  "links": [
    { "id": "111111" },
    { "id": "222222", "comments": "132" },
    { "id": "333333", "comments": "313", "both": true }
  ]
}
```

Responses preserve the original shapes and strings: JSON
`{"success":"4 links updated"}` / `{"error":"not authorized"}`, string-typed
read results (`{"id": "...", "lastvisit": "...", "comments": "...",
"commentvisit": "..."}`), the XML `<synccit>` documents, the plaintext
`id:lastvisit;count:commenttime,` rows, and the `X-API` / `X-Revision` /
`X-Success` / `X-Error` headers.

### Account API — `POST /account.php` (alias `/account`), `X-Revision: 1`

JSON/XML only. `login` (username + password) returns a long-lived login
token; with it: `devices` (list device codes), `addauth` (mint a device code,
no password needed), `delete` (remove device codes), `history` (last 20
visited links). `create` is gated exactly like the sync API.

### Apps API — `POST /api/apps.php` (alias `/apps`)

JSON only. Optional `data` of `{"platform":"android"|"ios"|"pc"|"all"}`
returns the supported-apps list from the `apps` table (`[]` when empty).

## Passwords and legacy import

New passwords are hashed with **argon2id** (m=64 MiB, t=3, p=2, PHC-encoded).

The original PHP app hashed with PBKDF2-HMAC-SHA512 (10000 iterations), with
the salt and hash stored split across two columns (`salt`, `passhash`). To
import a legacy user, insert one row with the combined stored format:

```sql
INSERT INTO user (username, password_hash, email, created, createdby)
VALUES ('james',
        'pbkdf2-sha512$10000$<salt-column-value>$<passhash-column-value>',
        'james@example.com', 1357891889, 'import');
```

Important: the original code feeds the stored base64 salt *string itself* as
the PBKDF2 salt input — this implementation reproduces that exactly, so
imported hashes verify as-is. On the first successful password check (sync
`addauth`, password-as-auth fallback, or account `login`), the row is
transparently rehashed to argon2id.

## Development

```sh
go test ./...
go vet ./...
CGO_ENABLED=0 go build ./...
```

Layout: `cmd/synccit` (entrypoint), `internal/config` (env config),
`internal/store` (SQLite schema + queries), `internal/auth` (password
hashing, codes, validation), `internal/api` (the three APIs),
`internal/cli` (serve + admin subcommands).

## Credits

Port of synccit by James Wilson / drakeapps
(https://github.com/drakeapps/synccit), GPL-3.0-or-later. This port preserves
the original API behavior and error strings for client compatibility.
