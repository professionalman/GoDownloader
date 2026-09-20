//go:build production

package web

import "embed"

// Assets embeds the production build artifacts of the React web frontend (web/dist).
//
//go:embed all:dist
var Assets embed.FS
