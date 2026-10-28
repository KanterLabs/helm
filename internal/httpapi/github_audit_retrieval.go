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
	hints := retrievalHints{paths: paths}
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

func (r *retrievalRun) step(w *retrievalWork) error {
	if err := r.fence(); err != nil {
		return err
	}
	if r.evidence.AssessmentVersion == store.GithubAssessmentVersion {
		if refs := store.GithubAuditObservedChecks(*r.evidence, w.task, w.condition); len(refs) > 0 {
			w.evidenceIDs = refs
			w.done = true
			return nil
		}
	}
	if !w.initialized {
		w.initialized = true
		if w.source.problem != "" {
			w.problem = w.source.problem
			w.done = true
			return nil
		}
		w.required = appendUnique(w.required, w.anchors.paths...)
		w.remaining = appendUnique(w.remaining, w.anchors.terms...)
		w.queue = appendUnique(w.queue, w.anchors.paths...)
		w.queue = appendUnique(w.queue, w.source.preferred...)
		for _, term := range w.anchors.terms {
			hints, err := r.discover(w.source, w.task.ID, term)
			if err != nil {
				return err
			}
			w.queue = appendUnique(w.queue, hints.paths...)
			if hints.problem != "" {
				w.problem = hints.problem
			}
		}
		if len(w.anchors.paths) == 0 && len(w.anchors.terms) == 0 {
			if _, err := r.plan(w); err != nil {
				return err
			}
		}
		w.queue = appendUnique(w.queue, likelyOwners(w.source.tree, len(w.anchors.terms) > 0)...)
		w.rounds = 1
	}
	var entry *githubprovider.TreeEntry
	for len(w.queue) > 0 {
		name := w.queue[0]
		w.queue = w.queue[1:]
		if w.seen[name] {
			continue
		}
		w.seen[name] = true
		for i := range w.source.tree {
			if w.source.tree[i].Path == name {
				entry = &w.source.tree[i]
				break
			}
		}
		if entry != nil {
			break
		} // default-branch hint absent from pinned tree is no proof
	}
	if entry == nil {
		if !w.implementation {
			planned, err := r.plan(w)
			if err != nil {
				return err
			}
			if planned {
				return nil
			}
		}
		w.done = true
		return nil
	}
	read, err := r.read(w.source, w.task.ID, *entry)
	if err != nil {
		return err
	}
	if read.problem != "" {
		w.problem = read.problem
		w.problemPath = entry.Path
		if strings.Contains(read.problem, "budget") {
			w.done = true
		}
		return nil
	}
	w.inspected = appendUnique(w.inspected, entry.Path)
	w.required = removeValue(w.required, entry.Path)
	lines := relevantLines(read.file.Text, w.anchors.terms)
	if sourceClass(entry.Path) == "implementation" {
		lines = implementationLines(read.file.Text, w.anchors.terms)
	}
	explicit := false
	for _, p := range w.anchors.paths {
		if p == entry.Path {
			explicit = true
		}
	}
	if planned, ok := w.semantic[entry.Path]; ok {
		count := len(strings.Split(read.file.Text, "\n"))
		if planned.StartLine > count {
			w.problem = "semantic_range_unavailable"
			return nil
		}
		lines = append(lines, planned.StartLine-1)
		if planned.EndLine <= count {
			lines = append(lines, planned.EndLine-1)
		}
		explicit = true
	}
	if len(lines) == 0 && !explicit {
		return nil
	}
	if len(lines) == 0 {
		lines = []int{0}
	}
	class := sourceClass(entry.Path)
	ids, limited := r.excerpts(w, read.file, lines, class)
	w.evidenceIDs = appendUnique(w.evidenceIDs, ids...)
	if limited {
		w.problem = "excerpt_budget_exhausted"
		w.problemPath = entry.Path
		w.done = true
		return nil
	}
	if class != "implementation" {
		return nil
	}
	w.implementation = true
	for _, term := range append([]string{}, w.remaining...) {
		if len(implementationLines(read.file.Text, []string{term})) > 0 {
			w.remaining = removeValue(w.remaining, term)
		}
	}
	for _, term := range w.pending {
		if regexpDefinition(term).MatchString(read.file.Text) {
			w.pending = removeValue(w.pending, term)
		}
	}
	if w.rounds < r.budget.RefinementRounds {
		refs := sourceReferences(read.file.Text, lines, w.anchors.terms)
		for _, ref := range refs {
			// A definition already present in this excerpt needs no network hint.
			definition := regexpDefinition(ref)
			if at := definition.FindStringIndex(read.file.Text); at != nil {
				refLines := []int{strings.Count(read.file.Text[:at[0]], "\n")}
				more, limited := r.excerpts(w, read.file, refLines, class)
				w.evidenceIDs = appendUnique(w.evidenceIDs, more...)
				if limited {
					w.problem = "excerpt_budget_exhausted"
					w.problemPath = entry.Path
					w.done = true
					return nil
				}
				continue
			}
			w.pending = appendUnique(w.pending, ref)
			hints, err := r.discover(w.source, w.task.ID, ref)
			if err != nil {
				return err
			}
			w.queue = appendUnique(hints.paths, w.queue...)
			w.anchors.terms = appendUnique(w.anchors.terms, ref)
		}
		w.rounds++
	}
	// Once an implementation owner and its references were inspected, unrelated
	// fallback files cannot improve this named retrieval gap.
	if len(w.pending) == 0 && len(w.required) == 0 && len(w.remaining) == 0 {
		ready, err := r.semanticOwnersReady(w)
		if err != nil {
			return err
		}
		w.done = ready
	}
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
