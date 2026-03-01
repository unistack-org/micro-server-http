package swagger_handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestHandlerNilDst(t *testing.T) {
	data := []byte("openapi: 3.0.0\ninfo:\n  title: Test")
	fsys := fstest.MapFS{
		"swagger.yaml": &fstest.MapFile{
			Data: data,
		},
	}

	h := Handler(nil, fsys)

	req := httptest.NewRequest(http.MethodGet, "/swagger.yaml", nil)
	w := httptest.NewRecorder()
	h(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, data, w.Body.Bytes())
}

func TestHandlerMergeDst(t *testing.T) {
	data := []byte("openapi: 3.0.0\ninfo:\n  title: Test")
	fsys := fstest.MapFS{
		"swagger.yaml": &fstest.MapFile{
			Data: data,
		},
	}

	dst := map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{"url": "https://api.example.com"},
		},
	}

	h := Handler(dst, fsys)

	req := httptest.NewRequest(http.MethodGet, "/swagger.yaml", nil)
	w := httptest.NewRecorder()
	h(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "api.example.com")
}
