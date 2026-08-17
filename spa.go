package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist
var webFS embed.FS

func SPAHandler() http.Handler {
	sub, err := fs.Sub(webFS, "dist")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
