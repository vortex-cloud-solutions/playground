// Package modules embeds every module's named queries and schema, so the
// API binary carries them and adding a query needs no Go code.
package modules

import "embed"

// FS holds <module>/queries/<name>.sql and <module>/schema.sql.
//
//go:embed */queries/*.sql */schema.sql
var FS embed.FS
