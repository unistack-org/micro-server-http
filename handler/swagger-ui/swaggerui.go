package swaggerui_handler

import (
	"embed"
	"html/template"
	"net/http"
	"path"
	"reflect"
)

//go:embed *.js *.css *.html *.png
var assets embed.FS

var (
	Handler = func(prefix string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || path.Base(r.URL.Path) != "swagger-initializer.js" {
				http.StripPrefix(prefix, http.FileServer(http.FS(assets))).ServeHTTP(w, r)
				return
			}
			tpl := template.New("swagger-initializer.js").Funcs(TemplateFuncs)
			ptpl, err := tpl.Parse(Template)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
			if err := ptpl.Execute(w, Config); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
		}
	}
	TemplateFuncs = template.FuncMap{
		"isInt": func(i interface{}) bool {
			v := reflect.ValueOf(i)
			switch v.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
				return true
			default:
				return false
			}
		},
		"isBool": func(i interface{}) bool {
			v := reflect.ValueOf(i)
			switch v.Kind() {
			case reflect.Bool:
				return true
			default:
				return false
			}
		},
		"isString": func(i interface{}) bool {
			v := reflect.ValueOf(i)
			switch v.Kind() {
			case reflect.String:
				return true
			default:
				return false
			}
		},
		"isSlice": func(i interface{}) bool {
			v := reflect.ValueOf(i)
			switch v.Kind() {
			case reflect.Slice:
				return true
			default:
				return false
			}
		},
		"isMap": func(i interface{}) bool {
			v := reflect.ValueOf(i)
			switch v.Kind() {
			case reflect.Map:
				return true
			default:
				return false
			}
		},
	}
	Template = `
	window.onload = function() {
		//<editor-fold desc="Changeable Configuration Block">
	
		window.ui = SwaggerUIBundle({
			{{- range $k, $v := . }}
			{{- if (eq (printf "%s" $v) "") -}}
			{{- continue -}}
			{{ end }}
			{{ $k }}: {{ if isBool $v -}}
			{{- $v -}},
			{{- else if isInt $v -}}
      {{- $v -}},
			{{- else if isString $v -}}
			"{{- $v -}}",
			{{- else if and (isSlice $v) (or (eq (printf "%s" $k) "presets") (eq (printf "%s" $k) "plugins")) -}}
			[
			{{- range $v }}
				{{ . }},
			{{- end }}
			],
			{{- end -}} 
			{{ end }}
		});
	
		//</editor-fold>
	};`
	Config = map[string]interface{}{
		"configUrl": "",
		"dom_id":    "#swagger-ui",
		/*
				"domNode":     "",
			  "spec":        "",
				"urls": []interface{}{
					map[string]interface{}{
							"url":  "",
							"name": "",
						},
					},
				},
		*/
		"url":                      "https://petstore.swagger.io/v2/swagger.json",
		"deepLinking":              true,
		"displayOperationId":       false,
		"defaultModelsExpandDepth": 1,
		"defaultModelExpandDepth":  1,
		"displayRequestDuration":   true,
		"filter":                   true,
		"operationsSorter":         "alpha",
		"showExtensions":           true,
		"tryItOutEnabled":          true,
		"presets": []string{
			"SwaggerUIBundle.presets.apis",
			"SwaggerUIStandalonePreset",
		},
		"plugins": []string{
			"SwaggerUIBundle.plugins.DownloadUrl",
		},
		"layout": "StandaloneLayout",
	}
)
