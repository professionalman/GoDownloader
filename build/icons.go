package build

import _ "embed"

//go:embed windows/icon.ico
var WindowsIcon []byte

//go:embed appicon.png
var AppIcon []byte
