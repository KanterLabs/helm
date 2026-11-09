package store

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The shared parity corpus (test/neglect/PARITY.md, plan D3 and case 15).
// Every fixture's rows go through the version 2 classifier exactly as the
// timeline iterator would hand them over. This test checks the expected
// values and the D2 shape itself; corpus_run.py then checks the same map
// against the recorded version 1 reference (rules 0 to 4).
var taskActivitySignalCorpusFiles = []string{"qualifying", "excluded", "ambiguous", "edges", "reclassified"}

type taskActivitySignalFixture struct {
	ID   string `json:"id"`
	Task struct {
		CreatedAt *string `json:"created_at"`
	} `json:"task"`
	Rows       []json.RawMessage          `json:"rows"`
	ExpectedV2 taskActivitySignalWireForm `json:"expected_v2"`
}

// taskActivitySignalWireForm is the corpus/runner shape of one result.
type taskActivitySignalWireForm struct {
	Status           string  `json:"status"`
	LastMeaningfulAt *string `json:"last_meaningful_at"`
	Source           *string `json:"source"`
	NotAfter         *string `json:"not_after"`
	Reason           *string `json:"reason"`
}

func taskActivitySignalWire(signal TaskActivitySignal) taskActivitySignalWireForm {
	text := func(value string) *string {
		if value == "" {
			return nil
		}
		return &value
	}
	at := func(value *time.Time) *string {
		if value == nil {
			return nil
		}
		formatted := value.UTC().Format(time.RFC3339Nano)
		return &formatted
	}
	return taskActivitySignalWireForm{Status: signal.Status, LastMeaningfulAt: at(signal.LastMeaningfulAt),
		Source: text(signal.Source), NotAfter: at(signal.NotAfter), Reason: text(signal.Reason)}
}

func sameSignalInstant(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left, errA := time.Parse(time.RFC3339Nano, *a)
	right, errB := time.Parse(time.RFC3339Nano, *b)
	return errA == nil && errB == nil && left.Equal(right)
}

func sameSignalText(a, b *string) bool {
	value := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	return value(a) == value(b)
}

func loadTaskActivitySignalCorpus(t *testing.T) (string, []taskActivitySignalFixture) {
	t.Helper()
	dir := filepath.Join("..", "..", "test", "neglect")
	var fixtures []taskActivitySignalFixture
	for _, name := range taskActivitySignalCorpusFiles {
		raw, err := os.ReadFile(filepath.Join(dir, "corpus", name+".json"))
		if err != nil {
			t.Fatalf("read corpus %s: %v", name, err)
		}
		var body struct {
			Corpus          string                      `json:"corpus"`
			CompleteHistory bool                        `json:"complete_history"`
			Fixtures        []taskActivitySignalFixture `json:"fixtures"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode corpus %s: %v", name, err)
		}
		if body.Corpus != "helm.neglect.timeline" || !body.CompleteHistory || len(body.Fixtures) == 0 {
			t.Fatalf("corpus %s header = %q complete=%v fixtures=%d", name, body.Corpus, body.CompleteHistory, len(body.Fixtures))
		}
		fixtures = append(fixtures, body.Fixtures...)
	}
	return dir, fixtures
}

func classifyTaskActivitySignalFixture(t *testing.T, fixture taskActivitySignalFixture) TaskActivitySignal {
	t.Helper()
	items := make([]TaskTimelineItem, 0, len(fixture.Rows))
	for index, raw := range fixture.Rows {
		var item TaskTimelineItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("%s row %d: %v", fixture.ID, index, err)
		}
		items = append(items, item)
	}
	createdAt := ""
	if fixture.Task.CreatedAt != nil {
		createdAt = *fixture.Task.CreatedAt
	}
	return classifyTaskActivity(createdAt, items, 0)
}

func TestTaskActivitySignalClassifierParityCorpus(t *testing.T) {
	dir, fixtures := loadTaskActivitySignalCorpus(t)
	results := make(map[string]taskActivitySignalWireForm, len(fixtures))
	for _, fixture := range fixtures {
		signal := classifyTaskActivitySignalFixture(t, fixture)
		got, want := taskActivitySignalWire(signal), fixture.ExpectedV2
		results[fixture.ID] = got
		if signal.ClassificationVersion != TaskActivityClassificationVersion || TaskActivityClassificationVersion != 2 {
			t.Errorf("%s: classification_version = %d", fixture.ID, signal.ClassificationVersion)
		}
		if got.Status != want.Status || !sameSignalInstant(got.LastMeaningfulAt, want.LastMeaningfulAt) ||
			!sameSignalText(got.Source, want.Source) || !sameSignalInstant(got.NotAfter, want.NotAfter) ||
			!sameSignalText(got.Reason, want.Reason) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			t.Errorf("%s:\n got  %s\n want %s", fixture.ID, gotJSON, wantJSON)
		}
		switch got.Status {
		case TaskActivityKnown:
			if !sameSignalInstant(got.NotAfter, got.LastMeaningfulAt) || got.Reason != nil || got.Source == nil {
				t.Errorf("%s: known shape %+v", fixture.ID, got)
			}
		case TaskActivityBounded:
			if got.LastMeaningfulAt != nil || got.Source != nil || got.NotAfter == nil || got.Reason == nil {
				t.Errorf("%s: bounded shape %+v", fixture.ID, got)
			}
		case TaskActivityUnknown:
			if got.NotAfter != nil || got.LastMeaningfulAt != nil || got.Reason == nil {
				t.Errorf("%s: unknown shape %+v", fixture.ID, got)
			}
		default:
			t.Errorf("%s: status %q", fixture.ID, got.Status)
		}
	}
	if t.Failed() {
		return
	}

	// Hand the same map to the Python runner so the v1 reference rules apply.
	output := os.Getenv("HELM_NEGLECT_V2_RESULTS")
	if output == "" {
		output = filepath.Join(t.TempDir(), "v2-results.json")
	}
	encoded, err := json.MarshalIndent(results, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("write v2 results: %v", err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; v1 reference rules not applied (run make neglect-corpus)")
	}
	run := exec.Command(python, filepath.Join(dir, "corpus_run.py"), "--v2-results", output)
	if combined, err := run.CombinedOutput(); err != nil {
		t.Fatalf("corpus_run.py --v2-results: %v\n%s", err, combined)
	}
}
