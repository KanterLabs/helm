package config

import (
	"fmt"
	"strings"
)

const (
	defaultAuditModel  = "gpt-5.6-sol"
	defaultAuditEffort = "medium"
)

// AuditProfile also supplies defaults to embedded callers that construct Config
// directly. It deliberately never falls back to ordinary Luna configuration.
// The HTTP start path captures these values in a new job; workers use that
// immutable capture, including for retrieval, rather than consulting Config.
func (c Config) AuditProfile() (model, effort string) {
	model, effort = c.AuditModel, c.AuditEffort
	if model == "" {
		model = defaultAuditModel
	}
	if effort == "" {
		effort = defaultAuditEffort
	}
	return model, effort
}

func validateAuditProfile(c Config) error {
	model, effort := c.AuditProfile()
	if len(model) > 128 || strings.ContainsAny(model, "\r\n\x00") {
		return fmt.Errorf("HELM_AUDIT_MODEL is invalid")
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max", "ultra":
		return nil
	default:
		return fmt.Errorf("HELM_AUDIT_EFFORT must be low, medium, high, xhigh, max, or ultra")
	}
}
