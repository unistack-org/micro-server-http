package pprof_handler

import (
	"expvar"
	"net/http"
	"net/http/pprof"
	"path"
	"strings"
)

func NewHandler(prefixPath string, initFuncs ...func()) http.HandlerFunc {
	for _, fn := range initFuncs {
		fn()
	}

	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.EqualFold(r.RequestURI, prefixPath) && r.RequestURI[len(r.RequestURI)-1] != '/':
			http.Redirect(w, r, r.RequestURI+"/", http.StatusMovedPermanently)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "cmdline")):
			pprof.Cmdline(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "profile")):
			pprof.Profile(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "symbol")):
			pprof.Symbol(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "trace")):
			pprof.Trace(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "goroutine")):
			pprof.Handler("goroutine").ServeHTTP(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "threadcreate")):
			pprof.Handler("threadcreate").ServeHTTP(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "mutex")):
			pprof.Handler("mutex").ServeHTTP(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "heap")):
			pprof.Handler("heap").ServeHTTP(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "block")):
			pprof.Handler("block").ServeHTTP(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "allocs")):
			pprof.Handler("allocs").ServeHTTP(w, r)
		case strings.HasPrefix(r.RequestURI, path.Join(prefixPath, "vars")):
			expvar.Handler().ServeHTTP(w, r)
		default:
			pprof.Index(w, r)
		}
	}
}
