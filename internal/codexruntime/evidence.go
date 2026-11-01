package codexruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

const maxEvidenceBytes = 512 * 1024

// DraftEvidence uses the requesting actor's existing CODEX_HOME credentials in
// a separate, short-lived app-server with no execution environment. It never
// reads, copies, exports, or otherwise handles actor credentials itself.
// Only the pinned executable/model pair proven by the synthetic transport
// probe is accepted. Ordinary Draft and account sessions retain their behavior.
func (m *Manager) DraftEvidence(ctx context.Context, actorID string, request RunRequest) (RunResult, error) {
	if request.Model != "gpt-5.6-luna" {
		return RunResult{}, fmt.Errorf("evidence inference requires the validated gpt-5.6-luna model")
	}
	if strings.TrimSpace(request.Prompt) == "" || len(request.Prompt) > maxEvidenceBytes {
		return RunResult{}, fmt.Errorf("evidence snapshot must contain 1 to %d bytes", maxEvidenceBytes)
	}
	if len(request.OutputSchema) == 0 || len(request.OutputSchema) > 32*1024 || !json.Valid(request.OutputSchema) {
		return RunResult{}, fmt.Errorf("evidence inference requires a valid bounded output schema")
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" || len(actorID) > 512 || strings.ContainsAny(actorID, "\r\n\x00") {
		return RunResult{}, fmt.Errorf("invalid actor id")
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return RunResult{}, ErrClosed
	}
	generation := m.EvidenceGeneration(actorID)
	version, err := exec.CommandContext(ctx, m.opts.Binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.0" {
		return RunResult{}, fmt.Errorf("evidence inference requires validated codex-cli 0.153.0")
	}
	opts := m.opts
	opts.evidenceOnly = true
	opts.MaxMessageBytes = maxEvidenceBytes
	session, err := startSession(ctx, actorID, opts)
	if err != nil {
		return RunResult{}, err
	}
	m.mu.Lock()
	if m.closed || m.evidenceGenerationLocked(actorID) != generation {
		m.mu.Unlock()
		_ = session.Close(context.Background())
		return RunResult{}, fmt.Errorf("evidence runtime closed or account changed before inference")
	}
	m.evidence[session] = struct{}{}
	m.mu.Unlock()
	defer func() {
		_ = session.Close(context.Background())
		m.mu.Lock()
		delete(m.evidence, session)
		m.mu.Unlock()
	}()
	// Empty maps merge with lower config layers. Refuse configured MCP servers
	// rather than pretending that an empty override removes their authority.
	var effective struct {
		Config *struct {
			MCPServers map[string]json.RawMessage `json:"mcp_servers"`
		} `json:"config"`
	}
	if err := session.Call(ctx, "config/read", map[string]any{"cwd": session.home, "includeLayers": false}, &effective); err != nil {
		return RunResult{}, fmt.Errorf("read evidence runtime configuration: %w", err)
	}
	if effective.Config == nil || len(effective.Config.MCPServers) != 0 {
		return RunResult{}, fmt.Errorf("evidence inference refuses configured MCP servers")
	}
	result, err := session.run(ctx, actorID, request, true)
	if err != nil {
		return result, err
	}
	if result.Status != "completed" || result.OutputTruncated || len(result.Output) > maxEvidenceBytes || !json.Valid([]byte(result.Output)) {
		return RunResult{}, fmt.Errorf("evidence inference returned invalid or incomplete JSON")
	}
	return result, nil
}

// Dotted keys are the supported app-server/CLI config override representation.
// environments:[] is additionally required on both thread and turn. Disabling
// shell_tool alone does not remove patch/image/environment capability.
func evidenceConfig() map[string]any {
	config := map[string]any{
		"web_search": "disabled", "project_doc_max_bytes": 0,
		"orchestrator.skills.enabled": false, "orchestrator.mcp.enabled": false,
		"tools.update_plan.enabled":                     false,
		"tools.experimental_request_user_input.enabled": false,
		"skills.include_instructions":                   false, "memories.use_memories": false,
		"include_environment_context": false, "include_apps_instructions": false,
		"include_collaboration_mode_instructions": false, "include_permissions_instructions": false,
	}
	for _, feature := range []string{
		"shell_tool", "shell_snapshot", "apps", "plugins", "multi_agent", "multi_agent_v2",
		"hooks", "memories", "skill_search", "skill_mcp_dependency_install",
		"browser_use", "browser_use_external", "computer_use", "image_generation",
		"code_mode_host", "code_mode", "code_mode_only", "tool_suggest", "recommended_plugins",
		"token_budget", "goals", "current_time_reminder", "sleep_tool", "deferred_executor",
	} {
		config["features."+feature] = false
	}
	config["features.skip_host_skill_discovery"] = true
	return config
}
