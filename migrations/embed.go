// Package migrations embeds the goose SQL migrations into the server binary.
package migrations

import "embed"

// FS holds the *.sql migrations, applied by the server at start.
//
//go:embed *.sql
var FS embed.FS
