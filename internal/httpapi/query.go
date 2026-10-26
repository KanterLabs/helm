package httpapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func parseLimit(r *http.Request, fallback int) (int, error) {
	values, present := r.URL.Query()["limit"]
	if !present {
		return fallback, nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return 0, errors.New("limit must be a positive integer")
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value <= 0 {
		return 0, errors.New("limit must be a positive integer")
	}
	if value > 200 {
		return 200, nil
	}
	return value, nil
}

func queryValue(r *http.Request, name string) (string, bool, error) {
	values, present := r.URL.Query()[name]
	if !present {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", true, fmt.Errorf("%s must be supplied at most once", name)
	}
	return values[0], true, nil
}

func parseOptionalBool(r *http.Request, name string) (bool, error) {
	value, present, err := queryValue(r, name)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean", name)
	}
}

// parseOptionalStrictBool is used by newer coordination filters whose wire
// contract permits only the lowercase JSON-style query spellings. Unlike the
// legacy helper above it does not trim or case-fold values.
func parseOptionalStrictBool(r *http.Request, name string) (bool, error) {
	value, present, err := queryValue(r, name)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func parseOptionalEnum(r *http.Request, name string, allowed map[string]struct{}) (string, error) {
	value, present, err := queryValue(r, name)
	if err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	if _, ok := allowed[value]; !ok {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

var semanticStates = map[string]struct{}{
	"backlog": {}, "ready": {}, "active": {}, "blocked": {}, "completed": {},
}

var taskPriorities = map[string]struct{}{
	"low": {}, "normal": {}, "high": {}, "urgent": {},
}

var taskKinds = map[string]struct{}{
	"task": {}, "bug": {},
}

var bugSeverities = map[string]struct{}{
	"s1": {}, "s2": {}, "s3": {}, "s4": {},
}

var bugSeverityFilters = map[string]struct{}{
	"s1": {}, "s2": {}, "s3": {}, "s4": {}, "untriaged": {},
}

var bugResolutions = map[string]struct{}{
	"fixed": {}, "duplicate": {}, "not_planned": {}, "cannot_reproduce": {}, "works_as_designed": {},
}

var bugResolutionFilters = map[string]struct{}{
	"fixed": {}, "duplicate": {}, "not_planned": {}, "cannot_reproduce": {}, "works_as_designed": {}, "unresolved": {},
}

func parseOptionalIdentifier(r *http.Request, name string) (string, error) {
	value, present, err := queryValue(r, name)
	if err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	if utf8.RuneCountInString(value) > 200 {
		return "", fmt.Errorf("%s is too long", name)
	}
	return value, nil
}

func parseOptionalSearch(r *http.Request) (string, error) {
	value, present, err := queryValue(r, "q")
	if err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	if utf8.RuneCountInString(value) > 200 {
		return "", errors.New("q is too long")
	}
	return value, nil
}

func parseOptionalTimestamp(r *http.Request, name string) (*time.Time, error) {
	value, present, err := queryValue(r, name)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("%s must be RFC3339", name)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("%s must be RFC3339", name)
	}
	return &parsed, nil
}

func parseAfterCursor(r *http.Request) (int64, error) {
	value, present, err := queryValue(r, "after")
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("after must be a cursor integer")
	}
	after, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.New("after must be a cursor integer")
	}
	if after < 0 {
		return 0, errors.New("after must be zero or greater")
	}
	return after, nil
}

func parsePagination(r *http.Request, fallback int) (int, int, error) {
	limit, err := parseLimit(r, fallback)
	if err != nil {
		return 0, 0, err
	}
	values, present := r.URL.Query()["cursor"]
	if !present {
		return limit, 0, nil
	}
	if len(values) != 1 {
		return 0, 0, errors.New("cursor must be supplied at most once")
	}
	offset, err := parseCursor(values[0])
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

func parseCursor(value string) (int, error) {
	if value == "" {
		return 0, errors.New("cursor must not be empty")
	}
	original := value
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		if number, parseErr := strconv.Atoi(string(decoded)); parseErr == nil && number >= 0 {
			return number, nil
		}
	}
	number, err := strconv.Atoi(original)
	if err != nil || number < 0 {
		return 0, errors.New("cursor must be a non-negative integer or URL-safe base64 offset")
	}
	return number, nil
}

func encodeCursor(value int) string {
	if value <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(value)))
}

func parseIfMatch(r *http.Request) (int64, error) {
	value := r.Header.Get("If-Match")
	if strings.TrimSpace(value) == "" {
		return 0, store.ErrPrecondition
	}
	if len(value) < 4 || value[0] != '"' || value[len(value)-1] != '"' || value[1] != 'v' {
		return 0, invalidIfMatch()
	}
	digits := value[2 : len(value)-1]
	if digits == "" || digits[0] == '0' {
		return 0, invalidIfMatch()
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, invalidIfMatch()
		}
	}
	parsed, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, invalidIfMatch()
	}
	return parsed, nil
}

func parseVersion(r *http.Request) (int64, error) {
	return parseIfMatch(r)
}

func invalidIfMatch() error {
	return &store.Error{Kind: store.ErrInvalid, Message: "If-Match must be a task version such as \"v3\""}
}
