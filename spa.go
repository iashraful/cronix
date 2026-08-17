package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var webFS embed.FS

func SPAHandler() http.Handler {
	sub, err := fs.Sub(webFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := fs.Stat(sub, p); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
