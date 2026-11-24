module github.com/i2y/ringi-server/examples

go 1.26.0

require (
	github.com/cedar-policy/cedar-go v1.8.0
	github.com/i2y/ringi-server v0.1.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

require (
	github.com/i2y/ringi-server/mcp v0.0.0
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/exp v0.0.0-20220921023135-46d9e7742f1e // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace github.com/i2y/ringi-server => ../

replace github.com/i2y/ringi-server/mcp => ../mcp
