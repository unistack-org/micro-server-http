package swagger // import "go.unistack.org/micro-server-http/v4/handler/swagger"

import (
	"context"
	"io/fs"
	"net/http"

	httpsrv "go.unistack.org/micro-server-http/v4"
	"go.unistack.org/micro/v4/server"
)

type (
	Hook         func([]byte) []byte
	ErrorHandler func(ctx context.Context, s server.Handler, w http.ResponseWriter, r *http.Request, err error, status int)
)

// Handler append to generated swagger data from dst map[string]interface{}
var Handler = func(fsys fs.FS, hooks []Hook, h httpsrv.ErrorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			h(r.Context())
			w.WriteHeader(http.StatusNotFound)
			return
		}

		path := r.URL.Path
		if len(path) > 1 && path[0] == '/' {
			path = path[1:]
		}

		buf, err := fs.ReadFile(fsys, path)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	}
}
