//go:build !production

package web

import "embed"

// Assets provides an empty embed.FS in non-production builds (e.g. tests, clean-checkout builds)
// to ensure reproducibility without requiring a pre-built web/dist.
var Assets embed.FS
