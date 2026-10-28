package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/KanterLabs/helm/internal/store"
)

var githubAssessmentOutputSchema = json.RawMessage(`{
 "type":"object","additionalProperties":false,"required":["assessment_version","assessments"],"properties":{
 "assessment_version":{"type":"integer","const":2},
 "assessments":{"type":"array","minItems":1,"maxItems":1000,"items":{
 "type":"object","additionalProperties":false,"required":["task_id","captured_version","source_column_id","conditions","placement","target_column_id","target_semantic_state","lifecycle_action","reason","basis"],"properties":{
 "task_id":{"type":"string"},"captured_version":{"type":"integer"},"source_column_id":{"type":"string"},
 "conditions":{"type":"array","maxItems":1000,"items":{"type":"object","additionalProperties":false,"required":["id","text","origin","result","explanation","citation_ids","gaps"],"properties":{
 "id":{"type":"string"},"text":{"type":"string"},"origin":{"type":"string"},"result":{"type":"string","enum":["supported","contradicted","unknown"]},"explanation":{"type":"string","maxLength":2000},"citation_ids":{"type":"array","maxItems":100,"items":{"type":"string"}},"gaps":{"type":"array","maxItems":20,"items":{"type":"string","maxLength":1000}}}}},
 "placement":{"type":"string","enum":["column_correct","correction_recommended","unable_to_verify"]},
 "target_column_id":{"type":["string","null"]},"target_semantic_state":{"type":["string","null"],"enum":["backlog","ready","active","blocked","completed",null]},
 "lifecycle_action":{"type":"string","enum":["none","complete","reopen","start","block","move","bug_resolve","bug_reopen"]},"reason":{"type":"string","maxLength":2000},
 "basis":{"type":"array","maxItems":10,"items":{"type":"object","additionalProperties":false,"required":["kind","explanation","citation_ids"],"properties":{"kind":{"type":"string","enum":["implementation","work_remaining","unstarted","ready","not_ready","blocker"]},"explanation":{"type":"string","maxLength":2000},"citation_ids":{"type":"array","maxItems":100,"items":{"type":"string"}}}}}
 }}}
 }}`)

func decodeGithubAssessmentOutput(raw string) ([]store.GithubAssessmentInput, error) {
	var output struct {
		Version     int                           `json:"assessment_version"`
		Assessments []store.GithubAssessmentInput `json:"assessments"`
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&output); err != nil {
		return nil, err
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("trailing model output")
	}
	if output.Version != store.GithubAssessmentVersion || len(output.Assessments) == 0 || len(output.Assessments) > 1000 {
		return nil, fmt.Errorf("invalid assessment version/scope")
	}
	for _, a := range output.Assessments {
		if a.Conditions == nil || a.Basis == nil || len(a.Conditions) > 1000 {
			return nil, fmt.Errorf("missing/oversized condition or basis collection")
		}
		for _, c := range a.Conditions {
			if c.CitationIDs == nil || c.Gaps == nil {
				return nil, fmt.Errorf("missing criterion citations/gaps")
			}
		}
	}
	return output.Assessments, nil
}
