// Package intake normalizes external alert deliveries into store alerts.
// Parsing is strict about the fields that drive correlation and bounded about
// everything copied into tasks; content never selects routing.
package intake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/KanterLabs/helm/internal/store"
)

const (
	CoolifyIntegration = "coolify"
	// MaxCoolifyBodyBytes bounds a webhook body. Real Coolify notices are a
	// few hundred bytes; this leaves room for many servers.
	MaxCoolifyBodyBytes = 64 << 10

	maxCoolifyServers  = 25
	maxResourceNameLen = 200
)

// Delivery dispositions that do not create alerts.
const (
	DispositionIgnored     = "ignored"
	DispositionUnsupported = "unsupported"
)

var (
	ErrInvalidPayload = errors.New("invalid coolify payload")

	eventPattern   = regexp.MustCompile(`^[a-z0-9_]{1,128}$`)
	uuidPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,32}$`)
	updatePattern  = regexp.MustCompile(`^[a-z_]{1,32}$`)
)

// CoolifyDelivery is a parsed webhook. Alerts is empty exactly when
// Disposition explains why the delivery creates no work.
type CoolifyDelivery struct {
	Event       string
	Disposition string
	Alerts      []store.IntakeAlert
}

type coolifyPayload struct {
	Event   string          `json:"event"`
	Servers json.RawMessage `json:"servers"`
}

type coolifyTraefikServer struct {
	Name              string `json:"name"`
	UUID              string `json:"uuid"`
	CurrentVersion    string `json:"current_version"`
	LatestVersion     string `json:"latest_version"`
	UpdateType        string `json:"update_type"`
	UpgradeTarget     string `json:"upgrade_target"`
	NewerBranchTarget string `json:"newer_branch_target"`
	NewerBranchLatest string `json:"newer_branch_latest"`
}

func invalidPayload(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPayload, fmt.Sprintf(format, args...))
}

// ParseCoolify validates one Coolify webhook body. Any invalid alert rejects
// the whole delivery so a multi-server notice never partially commits.
func ParseCoolify(body []byte) (CoolifyDelivery, error) {
	var payload coolifyPayload
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return CoolifyDelivery{}, invalidPayload("body must be a JSON object")
	}
	if decoder.More() {
		return CoolifyDelivery{}, invalidPayload("body must contain one JSON object")
	}
	if !eventPattern.MatchString(payload.Event) {
		return CoolifyDelivery{}, invalidPayload("event is missing or malformed")
	}
	delivery := CoolifyDelivery{Event: payload.Event}
	switch payload.Event {
	case "test":
		delivery.Disposition = DispositionIgnored
		return delivery, nil
	case "traefik_version_outdated":
		alerts, err := parseTraefikServers(payload.Servers)
		if err != nil {
			return CoolifyDelivery{}, err
		}
		delivery.Alerts = alerts
		return delivery, nil
	default:
		delivery.Disposition = DispositionUnsupported
		return delivery, nil
	}
}

func parseTraefikServers(raw json.RawMessage) ([]store.IntakeAlert, error) {
	var servers []coolifyTraefikServer
	if len(raw) == 0 || json.Unmarshal(raw, &servers) != nil {
		return nil, invalidPayload("servers must be an array of server objects")
	}
	if len(servers) == 0 || len(servers) > maxCoolifyServers {
		return nil, invalidPayload("servers must contain 1-%d entries", maxCoolifyServers)
	}
	alerts := make([]store.IntakeAlert, 0, len(servers))
	seen := make(map[string]struct{}, len(servers))
	for index, server := range servers {
		alert, err := traefikAlert(server)
		if err != nil {
			return nil, invalidPayload("servers[%d]: %v", index, err)
		}
		if _, duplicate := seen[alert.ConditionKey]; duplicate {
			continue
		}
		seen[alert.ConditionKey] = struct{}{}
		alerts = append(alerts, alert)
	}
	return alerts, nil
}

func traefikAlert(server coolifyTraefikServer) (store.IntakeAlert, error) {
	if !uuidPattern.MatchString(server.UUID) {
		return store.IntakeAlert{}, errors.New("uuid is missing or malformed")
	}
	if !versionPattern.MatchString(server.CurrentVersion) || !versionPattern.MatchString(server.LatestVersion) {
		return store.IntakeAlert{}, errors.New("current_version and latest_version are required versions")
	}
	if server.UpdateType != "" && !updatePattern.MatchString(server.UpdateType) {
		return store.IntakeAlert{}, errors.New("update_type is malformed")
	}
	for _, optional := range []string{server.UpgradeTarget, server.NewerBranchTarget, server.NewerBranchLatest} {
		if optional != "" && !versionPattern.MatchString(optional) {
			return store.IntakeAlert{}, errors.New("version targets are malformed")
		}
	}
	name := sanitizeName(server.Name)
	if name == "" {
		name = server.UUID
	}
	evidence := map[string]string{
		"current_version": server.CurrentVersion,
		"latest_version":  server.LatestVersion,
	}
	lines := []string{
		fmt.Sprintf("Coolify reported that Traefik on **%s** is outdated.", escapeMarkdown(name)),
		"",
		fmt.Sprintf("- Installed: `%s`", server.CurrentVersion),
		fmt.Sprintf("- Offered: `%s`", server.LatestVersion),
	}
	if server.UpdateType != "" {
		evidence["update_type"] = server.UpdateType
		lines = append(lines, "- Update type: "+strings.ReplaceAll(server.UpdateType, "_", " "))
	}
	if server.UpgradeTarget != "" {
		evidence["upgrade_target"] = server.UpgradeTarget
		lines = append(lines, fmt.Sprintf("- Upgrade target: `%s`", server.UpgradeTarget))
	}
	if server.NewerBranchTarget != "" {
		evidence["newer_branch_target"] = server.NewerBranchTarget
		line := fmt.Sprintf("- Newer branch: `%s`", server.NewerBranchTarget)
		if server.NewerBranchLatest != "" {
			evidence["newer_branch_latest"] = server.NewerBranchLatest
			line += fmt.Sprintf(" (latest `%s`)", server.NewerBranchLatest)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", "Versions are evidence from the Coolify alert, not an independently verified upgrade recommendation.")
	family := "traefik_version_outdated:server:" + server.UUID
	return store.IntakeAlert{
		AlertType:    "traefik_version_outdated",
		FamilyKey:    family,
		ConditionKey: family + ":latest:" + server.LatestVersion,
		ResourceName: name,
		ResourceID:   server.UUID,
		Title:        "Review Traefik update on " + name,
		Description:  strings.Join(lines, "\n"),
		Priority:     "normal",
		Evidence:     evidence,
	}, nil
}

// sanitizeName keeps a display name single-line and bounded.
func sanitizeName(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maxResourceNameLen {
		value = string(runes[:maxResourceNameLen])
	}
	return value
}

func escapeMarkdown(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if strings.ContainsRune("\\`*_[]()<>#!|~", r) {
			builder.WriteByte('\\')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
