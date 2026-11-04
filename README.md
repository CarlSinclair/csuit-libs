# CSuit-libs

Shared Go packages used across the CSuit products (CShop, CTime, CBots and TVT).

    go get github.com/CarlSinclair/csuit-libs@v1.0.0

Requires Go 1.26 or newer.

## Packages

- logger: leveled logging, secret and IP-to-location redaction, Sentry, Telegram alerts, persistent-failure and retry helpers
- mailer: SMTP delivery with STARTTLS, header sanitizing, attachments, text direction
- redis: client, queues, disk spool, email worker pool, rate limiting, delivery status, and a reliable queue (at-least-once delivery with a processing list and crash recovery)
- redisadmin: queue and key inspection helpers for operator pages
- secretbox: authenticated encryption for stored secrets (XChaCha20-Poly1305, HKDF-SHA256 per label, key IDs for rotation); also reads the legacy `enc:v1:` AES-GCM format
- session: database-backed sessions with a Redis cache
- sqlretry: retries for transient SQLite errors
- telegram: Bot API client with pacing, retry handling, token validation (`GetMe`) and login verification, plus one in-memory send queue per bot token
- telegram/durable: optional Redis-backed Telegram queue with a worker, so messages survive a restart

## Redis is optional

Nothing here needs a running Redis server to compile or to be imported. Without Redis:

- the Telegram per-token queue keeps working in memory
- logging, sessions (database only), secretbox, sqlretry and mailer work as normal

Only the email worker pool, `telegram/durable`, the reliable queue and Redis rate limiting need a Redis server.

## Examples

Sealing a stored secret, with a purpose label so a value sealed for one use cannot be opened as another:

    key, _ := secretbox.ParseKey(os.Getenv("SECRET_ENCRYPTION_KEY"))
    ring, _ := secretbox.New("k1", map[string][]byte{"k1": key})
    sealed, _ := ring.Seal("stripe/api-key", "sk_live_...")
    plain, err := ring.Open("stripe/api-key", sealed)

Rotation: add the new key to the keyring, make it current, and call `Reseal` on each stored value. `NeedsReseal` tells you which values still use an older key.

Checking a bot token and sending through the per-token queue:

    info, err := telegram.GetMe(ctx, token)
    err = telegram.Do(ctx, token, "notify", func(ctx context.Context, b *telegram.Bot) error {
        return b.SendMessage(ctx, chatID, "hello")
    })

## Security

Secret values are redacted from log output, and bot tokens are scrubbed from error text. Report vulnerabilities as described in SECURITY.md, not in public issues.

## License

AGPL-3.0-or-later. See LICENSE and NOTICE.
