// Package migrations embeds the SQL migration files so cmd/migrate ships as a
// single static binary with no dependency on the source tree being present at
// runtime (needed once this runs from a scratch/alpine container).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
