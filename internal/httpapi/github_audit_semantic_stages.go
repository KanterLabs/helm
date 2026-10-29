package httpapi

import "encoding/json"

// Single production owners, explicit anchors, linked PRs and v1 retain their
// existing route. Multiple unanchored owners require a concept before any file
// guess. This is a schema/server boundary, not optional advice to the planner.
func semanticSearchRequired(w *retrievalWork) bool {
	if w.semanticRounds != 0 || len(w.inspected) > 0 || len(w.anchors.paths)+len(w.anchors.terms)+len(w.source.preferred) != 0 {
		return false
	}
	owners := 0
	for _, entry := range w.source.tree {
		if sourceClass(entry.Path) == "implementation" {
			owners++
		}
	}
	return owners > 1
}

func semanticStageSchema(w *retrievalWork, searchOnly bool) json.RawMessage {
	if !w.searchFirst {
		return githubDiscoverySchema
	}
	var schema map[string]any
	_ = json.Unmarshal(githubDiscoverySchema, &schema)
	properties := schema["properties"].(map[string]any)
	paths := properties["paths"].(map[string]any)
	if searchOnly {
		delete(properties, "source_complete")
		schema["required"] = []string{"paths", "searches", "gap"}
		paths["maxItems"] = 0
		searches := properties["searches"].(map[string]any)
		searches["minItems"], searches["maxItems"] = 1, 1
	} else {
		properties["source_complete"] = map[string]any{"type": "boolean"}
		schema["required"] = []string{"paths", "searches", "gap", "source_complete"}
		item := paths["items"].(map[string]any)
		fields := item["properties"].(map[string]any)
		// Declaration requests contain no guessed coordinates. Other languages
		// and directory routing retain the existing bounded window contract.
		paths["items"] = map[string]any{"anyOf": []any{
			map[string]any{"type": "object", "additionalProperties": false,
				"required":   []string{"path", "declaration", "reason"},
				"properties": map[string]any{"path": fields["path"], "reason": fields["reason"], "declaration": map[string]any{"type": "string", "minLength": 1, "maxLength": 512}}},
			item,
		}}
	}
	out, _ := json.Marshal(schema)
	return out
}

func semanticStageInstructions(w *retrievalWork, searchOnly bool) string {
	if searchOnly {
		return "SEARCH-ONLY STAGE: Ownership is ambiguous. Return exactly one literal concept search and its criterion evidence question; paths MUST be empty. Filenames alone cannot establish ownership. Do not request a directory or source window in this stage. The server will intersect search hints with the pinned tree and expose selected source. This stage and the provider search consume the existing discovery budget."
	}
	if w.searchFirst {
		return "BODY-SELECTION STAGE: Inspect the provided pinned source and symbol index. For Go files return path, declaration and reason, using an exact function declaration, preferably the receiver-qualified selector displayed in symbols (Receiver.method). Omit start_line/end_line: the SERVER selects its entire bounded body. Bare function names must be unique. Metrics, constructors and line guesses are insufficient. For other languages or directory routing omit declaration and select an existing bounded window using start_line/end_line. Additional searches must name the literal owned function whose body answers a specific unanswered criterion question; a call site alone will not resolve that request. source_complete means no further source is needed, NOT that the criterion is satisfied. Empty paths/searches with source_complete=false means an unresolved evidence question and gap must name it. source_complete=true never erases missing search owners, budget limits or required source. Judge only the captured criterion; do not invent additional requirements."
	}
	return ""
}
