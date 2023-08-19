package spa

import (
	"io/fs"
	"net/http"
	"strings"
)

// Handler serve files from dir and redirect to index if file not exists
var Handler = func(prefix string, dir fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := http.StripPrefix(prefix, http.FileServer(http.FS(dir)))
		if _, err := fs.Stat(dir, strings.TrimPrefix(r.RequestURI, prefix)); err != nil {
			r.RequestURI = prefix
			r.URL.Path = prefix
		}
		f.ServeHTTP(w, r)
	}
}
