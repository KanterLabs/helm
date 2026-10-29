package httpapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// Limits are server-owned. They can be lowered by a capture caller, never by a
// task, repository, search result or model. The persisted budget makes stopping
// conditions reviewable. Provider methods have their own HTTP/page/size bounds.
func defaultRetrievalBudget() store.GithubAuditRetrievalBudget {
	return store.GithubAuditRetrievalBudget{PerCardFiles: 8, PerCardBytes: 256 * 1024, RunFiles: 96, RunReadBytes: 2 * 1024 * 1024, RunExcerptBytes: 120 * 1024, RunDiscoveryRequests: 48, PerCardDiscoveryRequests: 4, RefinementRounds: 2, ExcerptBytes: 6 * 1024, DurationSeconds: 90, IndexPlanningRounds: 1}
}

type retrievalSource struct {
	repository, ref, sha, role string
	indexPartial               bool
	tasks                      []store.GithubAuditTask
	preferred                  []string
	tree                       []githubprovider.TreeEntry
	problem                    string
}
type retrievalRead struct {
	file    githubprovider.File
	problem string
}
type retrievalHints struct {
	paths   []string
	problem string
}
type retrievalCardBudget struct {
	files                        map[string]bool
	bytes, discoveries, excerpts int
}
type retrievalRun struct {
	job                                  store.GithubAuditJob
	service                              *GithubAuditService
	ctx                                  context.Context
	check                                func() error
	evidence                             *store.GithubAuditEvidence
	budget                               store.GithubAuditRetrievalBudget
	reads                                map[string]retrievalRead
	hints                                map[string]retrievalHints
	cards                                map[string]*retrievalCardBudget
	items                                map[string]int
	readBytes, discoveries, excerptBytes int
}

func newRetrievalRun(g *GithubAuditService, ctx context.Context, check func() error, e *store.GithubAuditEvidence, b store.GithubAuditRetrievalBudget) *retrievalRun {
	e.RetrievalBudget = &b
	return &retrievalRun{service: g, ctx: ctx, check: check, evidence: e, budget: b, reads: map[string]retrievalRead{}, hints: map[string]retrievalHints{}, cards: map[string]*retrievalCardBudget{}, items: map[string]int{}}
}
func (r *retrievalRun) fence() error {
	if err := r.check(); err != nil {
		return err
	}
	return r.ctx.Err()
}
func (r *retrievalRun) card(id string) *retrievalCardBudget {
	if r.cards[id] == nil {
		r.cards[id] = &retrievalCardBudget{files: map[string]bool{}}
	}
	return r.cards[id]
}

func (r *retrievalRun) discover(source *retrievalSource, taskID, term string) (retrievalHints, error) {
	if err := r.fence(); err != nil {
		return retrievalHints{}, err
	}
	key := source.repository + ":" + term
	if hints, ok := r.hints[key]; ok {
		return hints, nil
	}
	card := r.card(taskID)
	if r.discoveries >= r.budget.RunDiscoveryRequests || card.discoveries >= r.budget.PerCardDiscoveryRequests {
		return retrievalHints{problem: "discovery_budget_exhausted"}, nil
	}
	d, ok := r.service.provider.(githubprovider.SourceDiscovery)
	if !ok {
		return retrievalHints{problem: "discovery_unavailable"}, nil
	}
	r.discoveries++
	card.discoveries++
	paths, _, err := d.FindPaths(r.ctx, source.repository, term)
	if fence := r.fence(); fence != nil {
		return retrievalHints{}, fence
	}
	hints := retrievalHints{paths: prioritizeDiscoveryPaths(paths)}
	if err != nil {
		hints.problem = "discovery_unavailable"
	}
	r.hints[key] = hints
	return hints, nil
}

func (r *retrievalRun) read(source *retrievalSource, taskID string, entry githubprovider.TreeEntry) (retrievalRead, error) {
	if err := r.fence(); err != nil {
		return retrievalRead{}, err
	}
	key := source.repository + "@" + source.sha + ":" + entry.Path
	card := r.card(taskID)
	if !card.files[key] && len(card.files) >= r.budget.PerCardFiles {
		return retrievalRead{problem: "file_budget_exhausted"}, nil
	}
	card.files[key] = true
	if cached, ok := r.reads[key]; ok {
		return cached, nil
	}
	if len(r.reads) >= r.budget.RunFiles || entry.Size < 0 || int64(r.readBytes)+entry.Size > int64(r.budget.RunReadBytes) || int64(card.bytes)+entry.Size > int64(r.budget.PerCardBytes) {
		return retrievalRead{problem: "read_budget_exhausted"}, nil
	}
	file, err := r.service.provider.ReadFile(r.ctx, source.repository, source.sha, entry.Path)
	if fence := r.fence(); fence != nil {
		return retrievalRead{}, fence
	}
	result := retrievalRead{file: file}
	if err != nil {
		result = retrievalRead{problem: "source_unavailable"}
	} else if file.SHA != entry.SHA || file.CommitSHA != source.sha || file.Repository != source.repository || file.Path != entry.Path || int64(len(file.Text)) != entry.Size {
		result = retrievalRead{problem: "source_identity_mismatch"}
	}
	// Count every attempted file, including denied or malformed responses. The
	// provider caps response bodies independently; no failures can refill a budget.
	r.readBytes += int(entry.Size)
	card.bytes += int(entry.Size)
	r.reads[key] = result
	return result, nil
}

type retrievalWork struct {
	semanticIndexRounds                                         int
	semanticDirectory                                           string
	semanticRounds                                              int
	searchOwners                                                map[string][]string
	searchTerms, searchPaths                                    []string
	semantic                                                    map[string]semanticPath
	source                                                      *retrievalSource
	task                                                        store.GithubAuditTask
	condition                                                   store.GithubAuditCondition
	anchors                                                     retrievalAnchors
	queue, inspected, evidenceIDs, pending, required, remaining []string
	seen                                                        map[string]bool
	initialized, done, implementation                           bool
	rounds                                                      int
	problem, problemPath                                        string
}

func (r *retrievalRun) retrieve(sources []*retrievalSource) error {
	// Read each exact tree once even when a baseline and linked PR share a SHA.
	trees := map[string]*retrievalSource{}
	queues := map[string][]*retrievalWork{}
	order := []string{}
	for _, source := range sources {
		key := source.repository + "@" + source.sha
		if previous := trees[key]; previous != nil {
			source.tree = previous.tree
			source.problem = previous.problem
			source.indexPartial = previous.indexPartial
		} else {
			if err := r.fence(); err != nil {
				return err
			}
			tree, partial, err := r.service.provider.ListFiles(r.ctx, source.repository, source.sha)
			if fence := r.fence(); fence != nil {
				return fence
			}
			source.tree = tree
			source.indexPartial = partial
			if err != nil {
				source.problem = "tree_unavailable"
			}
			if partial {
				r.evidence.Omissions = append(r.evidence.Omissions, "Source index is partial: "+key)
			}
			trees[key] = source
		}
		for _, task := range source.tasks {
			if _, ok := queues[task.ID]; !ok {
				order = append(order, task.ID)
			}
			if len(task.Conditions) == 0 {
				r.gap(source, task.ID, "", "ambiguous_criteria", "", "No labeled goal or acceptance conditions were captured; clarify completion meaning.")
				queues[task.ID] = queues[task.ID]
				continue
			}
			for _, condition := range task.Conditions {
				w := &retrievalWork{source: source, task: task, condition: condition, seen: map[string]bool{}}
				w.anchors = criterionAnchors(task, condition, source.tree)
				queues[task.ID] = append(queues[task.ID], w)
			}
		}
	}
	// One inspected file per card per turn, including across criteria and sources.
	// A card with many conditions cannot drain the run before a later card starts.
	for {
		active := false
		for _, id := range order {
			q := queues[id]
			if len(q) == 0 {
				continue
			}
			active = true
			w := q[0]
			if err := r.step(w); err != nil {
				return err
			}
			queues[id] = q[1:]
			if !w.done {
				queues[id] = append(queues[id], w)
			} else {
				r.finish(w)
			}
		}
		if !active {
			break
		}
	}
	r.sourceCoverage(sources)
	return nil
}

func removeValue(values []string, value string) []string {
	out := values[:0]
	for _, v := range values {
		if v != value {
			out = append(out, v)
		}
	}
	return out
}

func (r *retrievalRun) finish(w *retrievalWork) {
	r.evidence.CriterionCoverage = append(r.evidence.CriterionCoverage, store.GithubAuditCriterionCoverage{TaskID: w.task.ID, CriterionID: w.condition.ID, Repository: w.source.repository, SHA: w.source.sha, SourceRole: w.source.role, EvidenceIDs: w.evidenceIDs, InspectedPaths: w.inspected})
	if r.evidence.AssessmentVersion == store.GithubAssessmentVersion && len(store.GithubAuditObservedChecks(*r.evidence, w.task, w.condition)) > 0 {
		return
	}
	if w.implementation && len(w.pending) == 0 && len(w.required) == 0 && len(w.remaining) == 0 && !strings.Contains(w.problem, "budget") && !strings.HasPrefix(w.problem, "semantic_") && w.problem != "planning_input_limit" {
		return
	}
	code := w.problem
	if code == "" {
		code = "no_matching_implementation"
	}
	message := "No implementation owner was established in the pinned source; unread unrelated files are not criterion failures."
	if w.implementation && len(w.remaining) > 0 {
		code = "criterion_anchor_unresolved"
		message = "Required criterion anchors were not inspected in implementation: " + strings.Join(w.remaining, ", ")
	}
	if len(w.required) > 0 {
		code = "explicit_path_unavailable"
		w.problemPath = w.required[0]
		message = "A path explicitly required by this criterion was not inspected at the pinned commit."
	}
	if len(w.pending) > 0 {
		code = "source_reference_unresolved"
		message = "Implementation references need inspection: " + strings.Join(w.pending, ", ")
	}
	r.gap(w.source, w.task.ID, w.condition.ID, code, w.problemPath, message)
}
func (r *retrievalRun) gap(source *retrievalSource, taskID, criterionID, code, path, message string) {
	r.evidence.CriterionGaps = append(r.evidence.CriterionGaps, store.GithubAuditCriterionGap{TaskID: taskID, CriterionID: criterionID, Repository: source.repository, Ref: source.ref, SHA: source.sha, SourceRole: source.role, Path: path, Code: code, Message: message})
}

func retrievalDeadline(ctx context.Context, b store.GithubAuditRetrievalBudget) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(b.DurationSeconds)*time.Second)
}

func sourceKey(source *retrievalSource) string {
	return fmt.Sprintf("%s@%s:%s:%s", source.repository, source.sha, source.role, source.ref)
}
