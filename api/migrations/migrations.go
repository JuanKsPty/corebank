// Package migrations embeds the SQL schema into the binary.
//
// The .sql files stay next to each other in a directory anyone can read
// without going through Go, and embedding them means the container image needs
// nothing but the binary to bring an empty database up to date — no migration
// tool on the PATH, no files to mount, no drift between the image and the
// schema it expects.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
