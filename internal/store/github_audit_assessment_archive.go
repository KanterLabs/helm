package store

import "encoding/json"

func remapPortableAssessment(source *GithubAssessment, plan *portableImportPlan) *GithubAssessment {
	if source == nil {
		return nil
	}
	raw, _ := json.Marshal(source)
	var a GithubAssessment
	if json.Unmarshal(raw, &a) != nil {
		return nil
	}
	if v, ok := plan.taskMap[a.TaskID]; ok {
		a.TaskID = v
	}
	if v, ok := plan.columnMap[a.SourceColumnID]; ok {
		a.SourceColumnID = v
	}
	if a.TargetColumnID != nil {
		if v, ok := plan.columnMap[*a.TargetColumnID]; ok {
			a.TargetColumnID = &v
		}
	}
	return &a
}
