package codexruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var transportProbeNames = []string{
	"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
	"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
}

var transportBlockedNames = []string{
	"HELM_GITHUB_TOKEN", "HELM_GITHUB_TOKEN_FILE", "OPENAI_API_KEY",
	"CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "HELM_UNRELATED_SECRET",
	"NODE_OPTIONS", "BASH_ENV", "ENV", "SSLKEYLOGFILE",
}

type transportAttestation struct {
	Checks    map[string]bool `json:"checks"`
	Home      string          `json:"home"`
	CodexHome string          `json:"codexHome"`
}

// The protocol fixture checks what the spawned process actually received. It
// returns booleans only for server settings; no environment values are logged.
func evidenceTransportAttestation(mode string) string {
	proof := transportAttestation{Checks: make(map[string]bool), Home: os.Getenv("HOME"), CodexHome: os.Getenv("CODEX_HOME")}
	for _, name := range transportProbeNames {
		value, present := os.LookupEnv(name)
		switch mode {
		case "configured":
			proof.Checks[name] = present && value == "synthetic-transport-"+name
		case "empty":
			proof.Checks[name] = present && value == ""
		case "absent":
			proof.Checks[name] = !present
		}
	}
	for _, name := range transportBlockedNames {
		_, present := os.LookupEnv(name)
		proof.Checks["blocked:"+name] = !present
	}
	proof.Checks["PATH"] = os.Getenv("PATH") == "/synthetic-transport-path"
	encoded, _ := json.Marshal(proof)
	return string(encoded)
}

func TestDraftEvidenceTransportEnvironment(t *testing.T) {
	// Ordinary runtime fixtures launch this same test executable without a
	// test filter. Let their dedicated protocol helper own stdin/stdout.
	if os.Getenv("GO_WANT_CODEX_HELPER") == "1" || os.Getenv("HELM_EVIDENCE_HELPER") == "1" {
		return
	}
	for _, mode := range []string{"configured", "empty", "absent"} {
		t.Run(mode, func(t *testing.T) {
			for _, name := range transportProbeNames {
				value := ""
				if mode == "configured" {
					value = "synthetic-transport-" + name
				}
				t.Setenv(name, value)
				if mode == "absent" {
					if err := os.Unsetenv(name); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, name := range transportBlockedNames {
				t.Setenv(name, "synthetic-blocked-"+name)
			}
			t.Setenv("PATH", "/synthetic-transport-path")
			t.Setenv("HOME", "/synthetic-server-home")
			t.Setenv("CODEX_HOME", "/synthetic-server-codex-home")
			directory := t.TempDir()
			wrapper := filepath.Join(directory, "codex-transport-fixture")
			script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.153.0'; exit 0; fi\nHELM_EVIDENCE_HELPER=1 HELM_EVIDENCE_TRANSPORT_PROBE=" + mode + " exec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=^TestEvidenceHelperProcess$\n"
			if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(directory, "actors")
			manager := NewManager(Options{Binary: wrapper, HomeRoot: root, WorkingDir: directory, ShutdownGrace: 100 * time.Millisecond})
			defer manager.Close(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := manager.DraftEvidence(ctx, "transport-actor", RunRequest{
				Model: "gpt-5.6-luna", Effort: "medium", Prompt: "SYNTHETIC_TRANSPORT_PROBE",
				OutputSchema: json.RawMessage(`{"type":"object"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			var proof transportAttestation
			if err := json.Unmarshal([]byte(result.Output), &proof); err != nil {
				t.Fatal("invalid transport attestation")
			}
			if proof.Checks == nil {
				t.Fatal("missing transport attestation")
			}
			proof.Checks["actor:HOME"] = proof.Home == actorHome(root, "transport-actor")
			proof.Checks["actor:CODEX_HOME"] = proof.CodexHome == proof.Home
			proof.Checks["completed"] = result.Status == "completed"
			artifact, _ := json.Marshal(proof.Checks)
			fmt.Printf("evidence transport artifact (%s): %s\n", mode, artifact)
			for name, ok := range proof.Checks {
				if !ok {
					t.Errorf("child transport/isolation check failed: %s", name)
				}
			}
		})
	}
}
