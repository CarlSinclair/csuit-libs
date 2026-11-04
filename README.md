# CSuit-libs

Shared Go packages used across the CSuit products.

- logger: leveled logging, secret and IP-to-location redaction, Sentry, Telegram alerts, persistent-failure and retry helpers
- mailer: SMTP delivery with STARTTLS, header sanitizing, attachments, text direction
- redis: client, queues, disk spool, email worker pool, rate limiting, delivery status
- redisadmin: queue and key inspection helpers for operator pages
- secretbox: authenticated encryption for stored secrets (XChaCha20-Poly1305, HKDF, key IDs for rotation)
- session: database-backed sessions with a Redis cache
- sqlretry: retries for transient SQLite errors
- telegram: Bot API client with pacing, retry handling and login verification

Module: github.com/CarlSinclair/csuit-libs

License: AGPL-3.0-or-later. See LICENSE and NOTICE.
