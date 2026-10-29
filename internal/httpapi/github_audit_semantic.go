package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/KanterLabs/helm/internal/codexruntime"
)

type semanticPath struct {
	Path         string `json:"path"`
	Declaration  string `json:"declaration,omitempty"`
	declarations []string
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
	Reason       string `json:"reason"`
}

var githubDiscoverySchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["paths","searches","gap","source_complete"],"properties":{"source_complete":{"type":"boolean"},"gap":{"type":"string","minLength":1,"maxLength":1000},"searches":{"type":"array","maxItems":2,"items":{"type":"object","additionalProperties":false,"required":["term","question"],"properties":{"term":{"type":"string","minLength":3,"maxLength":200,"pattern":"^[A-Za-z0-9_./{}-]{3,200}$"},"question":{"type":"string","minLength":1,"maxLength":1000}}}},"paths":{"type":"array","maxItems":4,"items":{"type":"object","additionalProperties":false,"required":["path","start_line","end_line","reason"],"properties":{"path":{"type":"string","maxLength":512},"start_line":{"type":"integer","minimum":1,"maximum":1000000},"end_line":{"type":"integer","minimum":1,"maximum":1000000},"reason":{"type":"string","minLength":1,"maxLength":1000}}}}}}`)

// plan fills a named condition gap with paths from a server-owned pinned index.
// The model gets no provider credentials, tools, URLs or executable repository.
// Calls share TC-307 discovery/file/byte/deadline budgets and card scheduling.
func (r *retrievalRun) plan(w *retrievalWork) (bool, error) {
	if r.evidence.AssessmentVersion != 2 || r.service.runner == nil || w.semanticRounds >= r.budget.RefinementRounds {
		return false, nil
	}
	if err := r.fence(); err != nil {
		return false, err
	}
	card := r.card(w.task.ID)
	if card.discoveries >= r.budget.PerCardDiscoveryRequests || r.discoveries >= r.budget.RunDiscoveryRequests {
		w.problem = "discovery_budget_exhausted"
		return false, nil
	}
	if err := r.service.fresh(r.ctx, *r.evidence); err != nil {
		return false, err
	}
	if err := r.fence(); err != nil {
		return false, err
	}
	candidates, indexOnly := semanticIndex(w)
	searchOnly := semanticSearchRequired(w)
	w.searchFirst = w.searchFirst || searchOnly
	if indexOnly && w.semanticIndexRounds >= r.budget.IndexPlanningRounds {
		return false, nil
	}
	if len(candidates) == 0 {
		return false, nil
	}
	inspected := r.semanticInspected(w)
	prompt, err := json.Marshal(map[string]any{
		"phase": "discovery", "refinement": len(w.inspected) > 0,
		"instructions": githubDiscoveryInstructions,
		"task_id":      w.task.ID, "criterion": w.condition, "source": map[string]string{"repository": w.source.repository, "ref": w.source.ref, "sha": w.source.sha, "role": w.source.role}, "gap": "No implementation excerpt establishes this quoted condition: " + w.condition.Text,
		"candidates": candidates, "index_truncated": len(candidates) < len(w.source.tree), "directory_index": indexOnly, "inspected": inspected, "search_hints": w.searchPaths,
		"search_required": searchOnly, "stage_instructions": semanticStageInstructions(w, searchOnly),
	})
	if err != nil {
		return false, err
	}
	if len(prompt) > 80*1024 {
		w.problem = "planning_input_limit"
		return false, nil
	}
	r.discoveries++
	card.discoveries++
	if indexOnly && !searchOnly {
		w.semanticIndexRounds++
	} else {
		w.semanticRounds++
	}
	result, err := r.service.runner.DraftEvidence(r.ctx, r.job.AccountOwnerID, codexruntime.RunRequest{Prompt: string(prompt), Model: r.job.Model, Effort: r.job.Effort, OutputSchema: semanticStageSchema(w, searchOnly)})
	if fence := r.fence(); fence != nil {
		return false, fence
	}
	if err != nil || result.Status != "completed" || result.OutputTruncated || len(result.Output) > 16*1024 {
		return false, githubDiscoveryFailure("discovery_inference")
	}
	if err = r.service.fresh(r.ctx, *r.evidence); err != nil {
		return false, err
	}
	if err = r.fence(); err != nil {
		return false, err
	}
	var output struct {
		Paths          []semanticPath   `json:"paths"`
		Gap            string           `json:"gap"`
		Searches       []semanticSearch `json:"searches"`
		SourceComplete *bool            `json:"source_complete"`
	}
	d := json.NewDecoder(strings.NewReader(result.Output))
	d.DisallowUnknownFields()
	if err = d.Decode(&output); err != nil {
		return false, githubDiscoveryFailure("discovery_decode")
	}
	if d.Decode(new(any)) != io.EOF || len(output.Searches) > 2 || len(output.Paths) > 4 || output.Paths == nil || strings.TrimSpace(output.Gap) == "" || len(output.Gap) > 1000 {
		return false, githubDiscoveryFailure("discovery_schema")
	}
	for _, search := range output.Searches {
		if !validSemanticSearch(search) {
			return false, githubDiscoveryFailure("discovery_search")
		}
	}
	if searchOnly && (len(output.Searches) != 1 || len(output.Paths) != 0) {
		return false, githubDiscoveryFailure("discovery_search")
	}
	if w.searchFirst && !searchOnly && output.SourceComplete == nil {
		return false, githubDiscoveryFailure("discovery_schema")
	}
	planned := []string{}
	selectedDirectory := false
	for _, p := range output.Paths {
		member := false
		for _, entry := range candidates {
			member = member || entry.Path == p.Path
		}
		if !member {
			return false, githubDiscoveryFailure("discovery_path")
		}
		if p.Declaration == "" && (p.StartLine < 1 || p.EndLine < p.StartLine || p.EndLine-p.StartLine > 100 || p.EndLine > 1000000) {
			return false, githubDiscoveryFailure("discovery_range")
		}
		if strings.TrimSpace(p.Reason) == "" || len(p.Reason) > 1000 {
			return false, githubDiscoveryFailure("discovery_schema")
		}
		if len(p.Declaration) > 512 || w.searchFirst && !searchOnly && strings.HasSuffix(p.Path, ".go") && strings.TrimSpace(p.Declaration) == "" {
			return false, githubDiscoveryFailure("discovery_path")
		}
		if read, ok := r.reads[w.source.repository+"@"+w.source.sha+":"+p.Path]; ok && p.Declaration != "" {
			if _, _, valid := semanticSelectedBody(p, read.file.Text); !valid {
				return false, githubDiscoveryFailure("discovery_path")
			}
		}
		directory := false
		for _, candidate := range candidates {
			if candidate.Path == p.Path && candidate.Kind == "directory" {
				directory = true
			}
		}
		if directory {
			if w.semanticDirectory != "" || len(output.Paths) != 1 {
				return false, githubDiscoveryFailure("discovery_path")
			}
			w.semanticDirectory = p.Path
			selectedDirectory = true
			planned = append(planned, p.Path)
			continue
		}
		if indexOnly && w.semanticDirectory == "" {
			w.semanticDirectory = "."
		}
		if w.semantic == nil {
			w.semantic = map[string]semanticPath{}
		}
		if previous, ok := w.semantic[p.Path]; ok && previous.StartLine == p.StartLine && previous.EndLine == p.EndLine && previous.Declaration == p.Declaration {
			continue
		}
		if p.Declaration != "" {
			p.declarations = []string{p.Declaration}
			if previous, ok := w.semantic[p.Path]; ok && previous.Declaration != "" {
				p.declarations = appendUnique(append([]string{}, previous.declarations...), p.Declaration)
			}
		}
		w.semantic[p.Path] = p
		// Re-excerpting a cached exact file is allowed within excerpt/card budgets.
		delete(w.seen, p.Path)
		planned = appendUnique(planned, p.Path)
	}
	// A concept request did not expand a directory. Charge it to the existing
	// semantic rounds, leaving the directory-only round for actual expansion.
	if indexOnly && !searchOnly && len(output.Searches) > 0 && !selectedDirectory {
		w.semanticIndexRounds--
		w.semanticRounds++
	}
	searchPaths, err := r.semanticSearch(w, output.Searches)
	if err != nil {
		return false, err
	}
	planned = appendUnique(planned, searchPaths...)
	if len(planned) == 0 {
		w.semanticComplete = output.SourceComplete != nil && *output.SourceComplete
		if !searchOnly && (output.SourceComplete == nil || !*output.SourceComplete) {
			w.problem = "semantic_discovery_unresolved"
			w.semanticGap = output.Gap
		}
		return false, nil
	}
	if selectedDirectory {
		return r.plan(w)
	}
	w.queue = appendUnique(planned, w.queue...)
	return true, nil
}

// A bounded signature index helps the planner locate a body beyond a long
// package/import prefix without giving it a tool or another unpinned source.
var semanticSignature = regexp.MustCompile(`^\s*(?:export\s+)?(?:func|function|fn|def|class|type|interface|const)\s+`)
var semanticDeclaration = regexp.MustCompile(`^\s*((?:export\s+)?(?:func(?:\s+\([^)]*\))?|function|fn|def|class|type|interface|const)\s+[A-Za-z_$][A-Za-z0-9_$]*)`)

func semanticSymbols(text string) []map[string]any {
	out := []map[string]any{}
	declarations := semanticDeclarationLabels(text)
	block, firstLine := "", 0
	flush := func() {
		if block != "" {
			out = append(out, map[string]any{"line": firstLine, "signature": block})
		}
		block, firstLine = "", 0
	}
	for i, line := range strings.Split(text, "\n") {
		if !semanticSignature.MatchString(line) {
			continue
		}
		// Parameter lists consume the index budget without helping ownership.
		// Pack exact line-numbered declaration names into the same bounded
		// 40-by-160 signature envelope, preserving middle and late owners.
		if declaration := declarations[i+1]; declaration != "" {
			line = declaration
		} else if name := semanticDeclaration.FindStringSubmatch(line); name != nil {
			line = name[1]
		}
		entry := fmt.Sprintf("%d: %s", i+1, strings.TrimSpace(line))
		if len(entry) > 160 {
			entry = entry[:160]
		}
		if len(block)+len(entry)+1 > 160 {
			flush()
		}
		if block == "" {
			firstLine = i + 1
		} else {
			block += "\n"
		}
		block += entry
	}
	flush()
	// Extremely large owners retain the same ceiling and whole-file coverage.
	if len(out) > 40 {
		sampled := make([]map[string]any, 40)
		for i := range sampled {
			sampled[i] = out[i*(len(out)-1)/(len(sampled)-1)]
		}
		return sampled
	}
	return out
}

func (r *retrievalRun) semanticOwnersReady(w *retrievalWork) (bool, error) {
	if len(w.semantic) == 0 && len(w.searchTerms) == 0 {
		return true, nil
	}
	for p := range w.semantic {
		if !w.seen[p] {
			return false, nil
		}
	}
	if w.semanticRounds < r.budget.RefinementRounds {
		needsRefinement := len(w.semantic) == 0
		for p, plan := range w.semantic {
			read := r.reads[w.source.repository+"@"+w.source.sha+":"+p]
			// A deliberately selected whole file already exposes all behavior.
			// Partial windows need the opportunity to reject an ownership guess.
			lines := len(strings.Split(strings.TrimSuffix(read.file.Text, "\n"), "\n"))
			needsRefinement = needsRefinement || plan.StartLine != 1 || plan.EndLine < lines
		}
		if needsRefinement {
			planned, err := r.plan(w)
			if err != nil {
				return false, err
			}
			if planned {
				return false, nil
			}
		}
	}
	return true, nil
}
