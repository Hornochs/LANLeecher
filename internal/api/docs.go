package api

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var openAPIYAMLData []byte

var (
	specOnce sync.Once
	specJSON []byte
	specDoc  map[string]any
	specErr  error
)

func loadSpec() {
	specOnce.Do(func() {
		if specErr = yaml.Unmarshal(openAPIYAMLData, &specDoc); specErr != nil {
			return
		}
		specJSON, specErr = json.MarshalIndent(specDoc, "", "  ")
	})
}

// Spec returns the parsed OpenAPI document (for tests and llms-full.txt).
func Spec() (map[string]any, error) {
	loadSpec()
	return specDoc, specErr
}

func (a *API) openAPIJSON(w http.ResponseWriter, r *http.Request) {
	loadSpec()
	if specErr != nil {
		a.internal(w, r, specErr)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(specJSON)
}

func (a *API) openAPIYAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(openAPIYAMLData)
}

type docOp struct {
	Method, Path, Summary, Description, Scope string
	Params                                    []docParam
	Samples                                   []string
}

type docParam struct{ Name, In, Description, Type string }

var docsTmpl = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>LANLeecher API</title><link rel="stylesheet" href="/static/app.css"></head>
<body class="docs"><main class="container">
<h1>LANLeecher API v1</h1>
<p>Machine-readable spec: <a href="/api/openapi.json">openapi.json</a> · <a href="/api/openapi.yaml">openapi.yaml</a> ·
<a href="/llms.txt">llms.txt</a></p>
<p class="muted">Authentication: <code>Authorization: Bearer llk_…</code>. Bytes are integers, throughput is bit/s,
timestamps are RFC 3339 UTC, <code>bytes_down</code> = to the client, <code>bytes_up</code> = from the client.
Errors are <code>application/problem+json</code> (RFC 9457).</p>
{{range .}}<section class="card endpoint">
<h2><span class="method method-{{.Method}}">{{.Method}}</span> <code>{{.Path}}</code></h2>
<p><strong>{{.Summary}}</strong></p>{{if .Description}}<p class="muted pre">{{.Description}}</p>{{end}}
{{if .Params}}<table><thead><tr><th>Parameter</th><th>In</th><th>Type</th><th>Description</th></tr></thead><tbody>
{{range .Params}}<tr><td><code>{{.Name}}</code></td><td>{{.In}}</td><td>{{.Type}}</td><td>{{.Description}}</td></tr>{{end}}
</tbody></table>{{end}}
{{range .Samples}}<pre><code>{{.}}</code></pre>{{end}}
</section>{{end}}
</main></body></html>`))

func (a *API) docs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/" && r.URL.Path != "/api" {
		http.NotFound(w, r)
		return
	}
	loadSpec()
	if specErr != nil {
		a.internal(w, r, specErr)
		return
	}
	var ops []docOp
	paths, _ := specDoc["paths"].(map[string]any)
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, path := range keys {
		item, _ := paths[path].(map[string]any)
		for _, method := range []string{"get", "post"} {
			op, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			d := docOp{Method: strings.ToUpper(method), Path: path}
			d.Summary, _ = op["summary"].(string)
			d.Description, _ = op["description"].(string)
			params, _ := op["parameters"].([]any)
			for _, p := range params {
				pm, _ := p.(map[string]any)
				if ref, ok := pm["$ref"].(string); ok {
					pm = resolveRef(ref)
				}
				dp := docParam{}
				dp.Name, _ = pm["name"].(string)
				dp.In, _ = pm["in"].(string)
				dp.Description, _ = pm["description"].(string)
				if s, ok := pm["schema"].(map[string]any); ok {
					dp.Type, _ = s["type"].(string)
					if enum, ok := s["enum"].([]any); ok {
						var vals []string
						for _, e := range enum {
							b, _ := json.Marshal(e)
							vals = append(vals, string(b))
						}
						dp.Type += " (" + strings.Join(vals, ", ") + ")"
					}
				}
				d.Params = append(d.Params, dp)
			}
			samples, _ := op["x-codeSamples"].([]any)
			for _, s := range samples {
				if sm, ok := s.(map[string]any); ok {
					src, _ := sm["source"].(string)
					d.Samples = append(d.Samples, strings.TrimSpace(src))
				}
			}
			ops = append(ops, d)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := docsTmpl.Execute(w, ops); err != nil {
		a.Log.Error("docs", "err", err)
	}
}

func resolveRef(ref string) map[string]any {
	cur := any(specDoc)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	m, _ := cur.(map[string]any)
	return m
}
