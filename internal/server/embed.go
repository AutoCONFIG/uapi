package server

import (
	"embed"
)

//go:embed all:web
var webUIFS embed.FS
