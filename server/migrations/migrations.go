package migrations

import "embed"

// Files contains the ordered PostgreSQL migrations bundled into the API tools.
//
//go:embed *.sql
var Files embed.FS
