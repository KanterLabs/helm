package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/KanterLabs/helm/internal/store"
)

var githubAuditOutputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["findings"],"properties":{"findings":{"type":"array","maxItems":1000,"items":{"type":"object","additionalProperties":false,"required":["task_id","verdict","proposed_semantic_destination","confidence","reason","evidence_refs"],"properties":{"task_id":{"type":"string"},"verdict":{"type":"string","enum":["correct","needs_attention","move_proposed"]},"proposed_semantic_destination":{"type":["string","null"],"enum":["backlog","ready","active","blocked","completed",null]},"confidence":{"type":"number","minimum":0,"maximum":1},"reason":{"type":"string","maxLength":2000},"evidence_refs":{"type":"array","maxItems":100,"items":{"type":"string"}}}}}}}`)

func decodeGithubAuditOutput(raw string) ([]store.AuditFindingInput, error) {
	var output struct {
		Findings []struct {
			TaskID      string   `json:"task_id"`
			Verdict     string   `json:"verdict"`
			Destination *string  `json:"proposed_semantic_destination"`
			Confidence  float64  `json:"confidence"`
			Reason      string   `json:"reason"`
			Refs        []string `json:"evidence_refs"`
		} `json:"findings"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("trailing model output")
	}
	if len(output.Findings) > 1000 {
		return nil, fmt.Errorf("too many findings")
	}
	inputs := make([]store.AuditFindingInput, 0, len(output.Findings))
	for _, f := range output.Findings {
		inputs = append(inputs, store.AuditFindingInput{TaskID: f.TaskID, Verdict: f.Verdict, ProposedSemanticDestination: f.Destination, Confidence: f.Confidence, Reason: f.Reason, EvidenceRefs: f.Refs})
	}
	return inputs, nil
}
