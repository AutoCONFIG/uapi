package webstatic

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var dist embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return dist
	}
	return sub
}
