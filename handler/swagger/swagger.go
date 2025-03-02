package swagger_handler

import (
	"io/fs"
	"net/http"

	yamlcodec "go.unistack.org/micro-codec-yaml/v4"
	rutil "go.unistack.org/micro/v4/util/reflect"
)

// Handler append to generated swagger data from dst map[string]interface{}
var Handler = func(dst map[string]interface{}, fsys fs.FS) http.HandlerFunc {
	c := yamlcodec.NewCodec()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
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

		if dst == nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(buf)
			return
		}

		var src interface{}

		if err = c.Unmarshal(buf, src); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}

		if err = rutil.Merge(src, dst); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}

		if buf, err = c.Marshal(src); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	}
}
