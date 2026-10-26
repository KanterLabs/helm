package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestOpenAPIAssemblyRejectsMissingRootAndDuplicateDefinitions(t *testing.T) {
	for name, files := range map[string]fstest.MapFS{
		"missing root": {},
		"duplicate path": {
			"openapi/document.json": {Data: []byte(`{"openapi":"3.1.0","paths":{},"components":{}}`)},
			"openapi/tasks.json":    {Data: []byte(`{"paths":{"/tasks":{}}}`)},
			"openapi/other.json":    {Data: []byte(`{"paths":{"/tasks":{}}}`)},
		},
		"duplicate model": {
			"openapi/document.json": {Data: []byte(`{"openapi":"3.1.0","paths":{},"components":{}}`)},
			"openapi/tasks.json":    {Data: []byte(`{"components":{"schemas":{"Task":{}}}}`)},
			"openapi/other.json":    {Data: []byte(`{"components":{"schemas":{"Task":{}}}}`)},
		},
		"fragment metadata": {
			"openapi/document.json": {Data: []byte(`{"openapi":"3.1.0","paths":{},"components":{}}`)},
			"openapi/tasks.json":    {Data: []byte(`{"info":{"title":"override"}}`)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := assembleOpenAPI(files); err == nil {
				t.Fatal("expected invalid contract fragments to fail")
			}
		})
	}
}

func TestOpenAPIServedContractHasResolvableReferences(t *testing.T) {
	server, _ := testServer(t, "disabled")
	response := request(t, server, http.MethodGet, "/openapi.json", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("openapi status = %d", response.Code)
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["x-helm-fragments"]; exists {
		t.Fatal("authoring manifest leaked into served contract")
	}
	operations := map[string]bool{}
	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if id, ok := value["operationId"].(string); ok {
				if operations[id] {
					t.Errorf("duplicate operation ID %s", id)
				}
				operations[id] = true
			}
			if ref, ok := value["$ref"].(string); ok && strings.HasPrefix(ref, "#/") {
				var target any = document
				for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
					object, ok := target.(map[string]any)
					if !ok {
						t.Errorf("unresolved reference %s", ref)
						break
					}
					target, ok = object[part]
					if !ok {
						t.Errorf("unresolved reference %s", ref)
						break
					}
				}
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(document)
	if len(operations) == 0 {
		t.Fatal("served contract has no operations")
	}
}
