package swaggerui

import (
	"net/http"
	"testing"
)

func TestTemplate(t *testing.T) {
	t.Skip()
	h := http.NewServeMux()
	h.HandleFunc("/", Handler(""))
	if err := http.ListenAndServe(":8080", h); err != nil {
		t.Fatal(err)
	}
}
