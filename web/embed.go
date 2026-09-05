// Package web embeds the built single-page application. Run `pnpm build` (or
// `make web`) inside web/ before `go build` to refresh dist/.
package web

import "embed"

//go:embed all:dist
var Files embed.FS
