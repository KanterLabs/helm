package codexruntime

import (
	"strconv"
	"strings"
)

type evidenceAccountState struct {
	generation uint64
	connected  bool
}

// EvidenceGeneration fences work when authentication changes even if a logout
// and login return to the same account identity between worker account polls.
// It is process-local: restarted workers must fence interrupted job attempts.
func (m *Manager) EvidenceGeneration(actorID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.evidenceGenerationLocked(strings.TrimSpace(actorID))
}

func (m *Manager) evidenceGenerationLocked(actorID string) string {
	return strconv.FormatUint(m.accounts[actorID].generation, 10)
}

func (m *Manager) bumpEvidenceGeneration(actorID string) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" || len(actorID) > 512 || strings.ContainsAny(actorID, "\r\n\x00") {
		return
	}
	m.mu.Lock()
	state := m.accounts[actorID]
	state.generation++
	m.accounts[actorID] = state
	m.mu.Unlock()
}

func (m *Manager) observeEvidenceAccount(actorID string, connected bool) {
	actorID = strings.TrimSpace(actorID)
	m.mu.Lock()
	state := m.accounts[actorID]
	if state.connected && !connected {
		state.generation++
	}
	state.connected = connected
	m.accounts[actorID] = state
	m.mu.Unlock()
}
