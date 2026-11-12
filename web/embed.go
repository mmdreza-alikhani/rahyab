// Package web embeds the dashboard so the binary stays a single file.
package web

import "embed"

// FS holds the static dashboard assets.
//
//go:embed index.html app.js styles.css
var FS embed.FS
