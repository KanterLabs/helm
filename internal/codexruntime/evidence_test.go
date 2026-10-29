package codexruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This isolated protocol fixture was written before the evidence adapter.
// The separate Python transport probe verifies the real pinned executable.
func TestEvidenceHelperProcess(t *testing.T) {
	if os.Getenv("HELM_EVIDENCE_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	accountReads := 0
	for scanner.Scan() {
		var request struct {
			ID     *int64         `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.ID == nil {
			continue
		}
		result := any(map[string]any{})
		switch request.Method {
		case "account/read":
			accountReads++
			var account any = map[string]string{"type": "chatgpt", "email": "fixture@example.test", "planType": "plus"}
			if os.Getenv("HELM_EVIDENCE_ACCOUNT_SEQUENCE") == "1" && accountReads > 2 {
				account = nil
			}
			result = map[string]any{"account": account, "requiresOpenaiAuth": true}
		case "config/read":
			result = map[string]any{"config": map[string]any{"mcp_servers": map[string]any{}}}
		case "thread/start":
			config, _ := request.Params["config"].(map[string]any)
			environments, ok := request.Params["environments"].([]any)
			if !ok || len(environments) != 0 || config["orchestrator.skills.enabled"] != false || config["features.shell_tool"] != false || config["orchestrator.mcp.enabled"] != false || config["agents.enabled"] != false {
				_ = encoder.Encode(map[string]any{"id": *request.ID, "error": map[string]any{"code": -32602, "message": "unsafe evidence envelope"}})
				continue
			}
			result = map[string]any{"thread": map[string]string{"id": "evidence-thread"}}
		case "turn/start":
			environments, ok := request.Params["environments"].([]any)
			if !ok || len(environments) != 0 {
				os.Exit(2)
			}
			text := `{"ok":true}`
			if mode := os.Getenv("HELM_EVIDENCE_TRANSPORT_PROBE"); mode != "" {
				text = evidenceTransportAttestation(mode)
			}
			_ = encoder.Encode(map[string]any{"id": *request.ID, "result": map[string]any{"turn": map[string]string{"id": "evidence-turn"}}})
			_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]string{"type": "agentMessage", "text": text}}})
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]string{"id": "evidence-turn", "status": "completed"}}})
			continue
		}
		_ = encoder.Encode(map[string]any{"id": *request.ID, "result": result})
	}
	os.Exit(0)
}

func TestEvidenceAccountGeneration(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_HELPER") == "1" || os.Getenv("HELM_EVIDENCE_HELPER") == "1" {
		return
	}
	directory := t.TempDir()
	wrapper := filepath.Join(directory, "account-fixture")
	script := "#!/bin/sh\nHELM_EVIDENCE_HELPER=1 HELM_EVIDENCE_ACCOUNT_SEQUENCE=1 exec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=TestEvidenceHelperProcess\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{Binary: wrapper, HomeRoot: filepath.Join(directory, "actors"), ShutdownGrace: 100 * time.Millisecond})
	defer manager.Close(context.Background())
	ctx := context.Background()
	initial := manager.EvidenceGeneration("actor-a")
	for range 2 {
		status, err := manager.Account(ctx, "actor-a", false)
		if err != nil || !status.Connected {
			t.Fatalf("connected poll: %+v %v", status, err)
		}
		if got := manager.EvidenceGeneration("actor-a"); got != initial {
			t.Fatalf("unchanged connected poll advanced generation: %s", got)
		}
	}
	status, err := manager.Account(ctx, "actor-a", false)
	if err != nil || status.Connected {
		t.Fatalf("disconnected poll: %+v %v", status, err)
	}
	disconnected := manager.EvidenceGeneration("actor-a")
	if disconnected == initial {
		t.Fatal("disconnect did not fence evidence")
	}
	_, _ = manager.Account(ctx, "actor-a", false)
	if manager.EvidenceGeneration("actor-a") != disconnected {
		t.Fatal("repeated disconnected poll advanced generation")
	}
	_, _ = manager.StartDeviceLogin(ctx, "actor-a") // Deliberately invalid response still fences the attempt.
	login := manager.EvidenceGeneration("actor-a")
	if login == disconnected {
		t.Fatal("failed login attempt did not fence evidence")
	}
	if err := manager.LogoutAccount(ctx, "actor-a"); err != nil {
		t.Fatal(err)
	}
	if manager.EvidenceGeneration("actor-a") == login {
		t.Fatal("logout attempt did not fence evidence")
	}
	if manager.EvidenceGeneration("actor-b") != initial {
		t.Fatal("account generation crossed actors")
	}
	// Session creation fails, but the logout intent must be fenced before IO.
	manager.opts.Binary = filepath.Join(directory, "missing")
	prior := manager.EvidenceGeneration("actor-a")
	if err := manager.LogoutAccount(ctx, "actor-a"); err == nil {
		t.Fatal("missing executable unexpectedly succeeded")
	}
	if manager.EvidenceGeneration("actor-a") == prior {
		t.Fatal("failed logout attempt did not fence evidence")
	}
}

func TestDraftEvidenceIsolatedProtocol(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_HELPER") == "1" {
		return
	}
	directory := t.TempDir()
	wrapper := filepath.Join(directory, "codex-fixture")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.153.0'; exit 0; fi\nHELM_EVIDENCE_HELPER=1 exec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=TestEvidenceHelperProcess\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// Set only by the synthetic real-binary transport probe. This exercises
	// DraftEvidence itself against the pinned executable, with a local fake
	// model endpoint and disposable actor home; it never uses live accounts.
	if actual := os.Getenv("HELM_EVIDENCE_E2E_BINARY"); actual != "" {
		wrapper = actual
	}
	manager := NewManager(Options{Binary: wrapper, HomeRoot: filepath.Join(directory, "actors"), WorkingDir: directory, ShutdownGrace: 100 * time.Millisecond})
	defer manager.Close(context.Background())
	request := RunRequest{Model: "gpt-5.6-luna", Effort: "medium", Prompt: "SYNTHETIC_SNAPSHOT", OutputSchema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)}
	// The synthetic transport probe must exercise the requested model through
	// the adapter, not merely label a Luna request as a stronger-model proof.
	if model := os.Getenv("HELM_EVIDENCE_E2E_MODEL"); model != "" {
		request.Model = model
	}
	result, err := manager.DraftEvidence(context.Background(), "actor-a", request)
	if os.Getenv("HELM_EVIDENCE_EXPECT_VERSION_REJECTION") == "1" {
		if err == nil || !strings.Contains(err.Error(), "validated codex-cli") {
			t.Fatalf("expected unvalidated executable rejection, got %v", err)
		}
		fmt.Fprintln(os.Stdout, "evidence protocol artifact: unvalidated executable refused before inference")
		return
	}
	if os.Getenv("HELM_EVIDENCE_EXPECT_CONFIG_REJECTION") == "1" {
		if err == nil || !strings.Contains(err.Error(), "configured MCP") {
			t.Fatalf("expected unsafe configuration rejection, got %v", err)
		}
		fmt.Fprintln(os.Stdout, "evidence protocol artifact: configured MCP refused before inference")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != `{"ok":true}` || result.Status != "completed" {
		t.Fatalf("unexpected evidence result: %+v", result)
	}
	for _, invalid := range []RunRequest{
		{Model: "unverified-model", Prompt: request.Prompt, OutputSchema: request.OutputSchema},
		{Model: request.Model, Prompt: strings.Repeat("x", 512*1024+1), OutputSchema: request.OutputSchema},
		{Model: request.Model, Prompt: request.Prompt},
	} {
		if _, err := manager.DraftEvidence(context.Background(), "actor-a", invalid); err == nil {
			t.Fatal("unsafe input accepted")
		}
	}
	fmt.Fprintln(os.Stdout, "evidence protocol artifact: isolated environments; bounded schema input; unknown model refused")
}
