package httpapi

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
)

// The authored OpenAPI domains are assembled into bounded JSON assets by
// web/scripts/generate-openapi.mjs. Embedding the entire tree keeps discovery
// independent of the source checkout while retaining one public document.
//
//go:embed openapi
var openAPIAssets embed.FS

var openAPIDocument = mustOpenAPIDocument()

func mustOpenAPIDocument() []byte {
	document, err := assembleOpenAPI(openAPIAssets)
	if err != nil {
		panic(fmt.Sprintf("assemble embedded OpenAPI contract: %v", err))
	}
	return document
}

func assembleOpenAPI(assets fs.FS) ([]byte, error) {
	base, err := fs.ReadFile(assets, "openapi/document.json")
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(base, &document); err != nil {
		return nil, err
	}
	paths := map[string]json.RawMessage{}
	components := map[string]map[string]json.RawMessage{}
	if err := json.Unmarshal(document["paths"], &paths); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(document["components"], &components); err != nil {
		return nil, err
	}
	err = fs.WalkDir(assets, "openapi", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path == "openapi/document.json" {
			return nil
		}
		data, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		var fragment map[string]json.RawMessage
		if err := json.Unmarshal(data, &fragment); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for section, definitions := range fragment {
			switch section {
			case "paths":
				if err := addOpenAPIDefinitions(paths, definitions, path+" paths"); err != nil {
					return err
				}
			case "components":
				var categories map[string]json.RawMessage
				if err := json.Unmarshal(definitions, &categories); err != nil {
					return err
				}
				for category, values := range categories {
					if components[category] == nil {
						components[category] = map[string]json.RawMessage{}
					}
					if err := addOpenAPIDefinitions(components[category], values, path+" components/"+category); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("fragment %s cannot define %s", path, section)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// RawMessage retains examples and numeric values without lossy conversion.
	document["paths"], err = json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	document["components"], err = json.Marshal(components)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func addOpenAPIDefinitions(destination map[string]json.RawMessage, source json.RawMessage, label string) error {
	var definitions map[string]json.RawMessage
	if err := json.Unmarshal(source, &definitions); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if definitions == nil {
		return fmt.Errorf("%s must be an object", label)
	}
	for name, value := range definitions {
		if _, exists := destination[name]; exists {
			return fmt.Errorf("duplicate %s definition: %s", label, name)
		}
		destination[name] = value
	}
	return nil
}
