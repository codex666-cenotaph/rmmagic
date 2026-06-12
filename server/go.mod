module github.com/codex666-cenotaph/rmmagic/server

go 1.25.11

require github.com/codex666-cenotaph/rmmagic/shared v0.0.0

require (
	github.com/alexedwards/argon2id v1.0.0
	github.com/coder/websocket v1.8.14
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/pquerna/otp v1.5.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	golang.org/x/crypto v0.14.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.13.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/codex666-cenotaph/rmmagic/shared => ../shared
