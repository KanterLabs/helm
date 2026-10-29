package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/store"
)

// syntheticAI has no token-file path, process launcher or network client. Its
// only model input is the exact evidence request accepted by the real worker.
type syntheticAI struct{ state *syntheticState }

func (a *syntheticAI) Account(context.Context, string, bool) (codexruntime.AccountStatus, error) {
	return codexruntime.AccountStatus{Connected: true, AccountType: "chatgpt", PlanType: "synthetic"}, nil
}
func (a *syntheticAI) StartDeviceLogin(context.Context, string) (codexruntime.DeviceLogin, error) {
	return codexruntime.DeviceLogin{}, errors.New("synthetic account already connected")
}
func (a *syntheticAI) CancelDeviceLogin(context.Context, string, string) (codexruntime.CancelLoginResult, error) {
	return codexruntime.CancelLoginResult{Status: "cancelled"}, nil
}
func (a *syntheticAI) LogoutAccount(context.Context, string) error { return nil }

func (a *syntheticAI) DraftEvidence(ctx context.Context, actorID string, request codexruntime.RunRequest) (codexruntime.RunResult, error) {
	if strings.Contains(request.Prompt, sentinelToken) {
		return codexruntime.RunResult{}, errors.New("credential reached evidence boundary")
	}
	a.state.mu.Lock()
	a.state.aiCalls++
	a.state.lastAIActor = actorID
	delay := a.state.delayMS
	a.state.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return codexruntime.RunResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	var prompt struct {
		Evidence store.GithubAuditEvidence `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(request.Prompt), &prompt); err != nil {
		return codexruntime.RunResult{}, err
	}
	if len(prompt.Evidence.Items) == 0 {
		return codexruntime.RunResult{}, errors.New("missing real captured evidence")
	}
	if prompt.Evidence.AssessmentVersion == store.GithubAssessmentVersion {
		output, err := syntheticAssessments(prompt.Evidence)
		return codexruntime.RunResult{Status: "completed", Output: output}, err
	}
	ref := prompt.Evidence.Items[0].ID
	findings := []map[string]any{}
	for _, task := range prompt.Evidence.Tasks {
		verdict, reason := "move_proposed", "The captured ready.go source supports scheduling this task in Ready. Human approval and explicit apply are required."
		var destination any = "ready"
		if prompt.Evidence.Partial {
			verdict, reason, destination = "needs_attention", "Source coverage is partial; a human must resolve the omissions.", nil
		}
		findings = append(findings, map[string]any{"task_id": task.ID, "verdict": verdict,
			"proposed_semantic_destination": destination, "confidence": 0.88, "reason": reason, "evidence_refs": []string{ref}})
	}
	output, err := json.Marshal(map[string]any{"findings": findings})
	return codexruntime.RunResult{Status: "completed", Output: string(output)}, err
}
