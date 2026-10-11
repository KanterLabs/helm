package intake

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/KanterLabs/helm/internal/store"
)

// GenericTicketFields documents every accepted top-level field, in the order
// shown to users.
var GenericTicketFields = []string{"title", "description", "priority", "dedupe_key", "source", "url", "fields"}

const (
	maxGenericTitle       = 300
	maxGenericDescription = 20000
	maxGenericDedupeKey   = 200
	maxGenericFields      = 20
	maxGenericFieldValue  = 300
	maxGenericURL         = 1000
	maxGenericEvidence    = 8000
)

var (
	sourcePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,59}$`)
	fieldKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)
)

// FieldError is a payload problem tied to one field, so callers can tell an
// outside app exactly what to fix.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }
func (e *FieldError) Unwrap() error { return ErrInvalidPayload }

func fieldError(field, format string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

type genericTicket struct {
	Title       *string                    `json:"title"`
	Description *string                    `json:"description"`
	Priority    *string                    `json:"priority"`
	DedupeKey   *string                    `json:"dedupe_key"`
	Source      *string                    `json:"source"`
	URL         *string                    `json:"url"`
	Fields      map[string]json.RawMessage `json:"fields"`
}

// ParseGenericTicket validates the documented generic ticket JSON for one
// webhook. Without dedupe_key every post opens a new ticket; with it, posts
// while the ticket is open only count repeats, and a post after completion
// opens a new linked ticket.
func ParseGenericTicket(body []byte, webhookID, webhookName string) (store.IntakeAlert, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return store.IntakeAlert{}, fieldError("", "body must be a JSON object such as {\"title\": \"Disk almost full\"}")
	}
	for name := range raw {
		if !contains(GenericTicketFields, name) {
			return store.IntakeAlert{}, fieldError(name, "unknown field %q; allowed fields: %s", name, strings.Join(GenericTicketFields, ", "))
		}
	}
	var payload genericTicket
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return store.IntakeAlert{}, fieldError(typeErrorField(err), "%s has the wrong type: %s", typeErrorField(err), "expected text (fields must be an object)")
	}
	if payload.Title == nil || strings.TrimSpace(*payload.Title) == "" {
		return store.IntakeAlert{}, fieldError("title", "title is required")
	}
	title := sanitizeName(*payload.Title)
	if utf8.RuneCountInString(title) > maxGenericTitle {
		return store.IntakeAlert{}, fieldError("title", "title must be at most %d characters", maxGenericTitle)
	}
	alert := store.IntakeAlert{
		AlertType:             "webhook",
		ResourceName:          webhookName,
		ResourceID:            webhookID,
		Title:                 title,
		Priority:              "normal",
		Evidence:              map[string]string{},
		ReopenAfterCompletion: true,
	}
	if payload.Description != nil {
		description := strings.TrimSpace(*payload.Description)
		if utf8.RuneCountInString(description) > maxGenericDescription {
			return store.IntakeAlert{}, fieldError("description", "description must be at most %d characters", maxGenericDescription)
		}
		alert.Description = description
	}
	if payload.Priority != nil {
		switch *payload.Priority {
		case "low", "normal", "high", "urgent":
			alert.Priority = *payload.Priority
		default:
			return store.IntakeAlert{}, fieldError("priority", "priority must be one of low, normal, high, urgent")
		}
	}
	if payload.Source != nil {
		if !sourcePattern.MatchString(*payload.Source) {
			return store.IntakeAlert{}, fieldError("source", "source must be 1-60 letters, digits, spaces, dots, dashes or underscores")
		}
		alert.AlertType = *payload.Source
	}
	if payload.URL != nil {
		parsed, err := url.Parse(*payload.URL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || len(*payload.URL) > maxGenericURL {
			return store.IntakeAlert{}, fieldError("url", "url must be an http(s) link of at most %d characters", maxGenericURL)
		}
		alert.Evidence["link"] = *payload.URL
	}
	if len(payload.Fields) > maxGenericFields {
		return store.IntakeAlert{}, fieldError("fields", "fields may contain at most %d entries", maxGenericFields)
	}
	for key, value := range payload.Fields {
		if !fieldKeyPattern.MatchString(key) || key == "link" {
			return store.IntakeAlert{}, fieldError("fields."+key, "field names must be 1-40 letters, digits, spaces, dots, dashes or underscores (and not \"link\")")
		}
		text, ok := scalarText(value)
		if !ok {
			return store.IntakeAlert{}, fieldError("fields."+key, "fields.%s must be text, a number or true/false", key)
		}
		if utf8.RuneCountInString(text) > maxGenericFieldValue {
			return store.IntakeAlert{}, fieldError("fields."+key, "fields.%s must be at most %d characters", key, maxGenericFieldValue)
		}
		alert.Evidence[key] = text
	}
	if encoded, _ := json.Marshal(alert.Evidence); len(encoded) > maxGenericEvidence {
		return store.IntakeAlert{}, fieldError("fields", "fields and url together must be under %d bytes", maxGenericEvidence)
	}
	family := ""
	if payload.DedupeKey != nil {
		key := strings.TrimSpace(*payload.DedupeKey)
		if key == "" || utf8.RuneCountInString(key) > maxGenericDedupeKey || strings.ContainsAny(key, "\r\n\x00") {
			return store.IntakeAlert{}, fieldError("dedupe_key", "dedupe_key must be 1-%d characters on one line", maxGenericDedupeKey)
		}
		family = "dedupe:" + key
	} else {
		// No key: every delivery is its own ticket.
		family = "delivery:" + newDeliveryID()
	}
	alert.FamilyKey = family
	alert.ConditionKey = family
	return alert, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func typeErrorField(err error) string {
	if typeErr, ok := err.(*json.UnmarshalTypeError); ok && typeErr.Field != "" {
		return typeErr.Field
	}
	return "body"
}

func scalarText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return sanitizeName(text), true
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&number) == nil {
		return number.String(), true
	}
	var flag bool
	if json.Unmarshal(raw, &flag) == nil {
		return strconv.FormatBool(flag), true
	}
	return "", false
}

func newDeliveryID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable")
	}
	return hex.EncodeToString(buf)
}
