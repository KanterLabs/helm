package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

func decodeJSON(r *http.Request, value any) error {
	_, err := decodeJSONObject(r, value)
	return err
}

// decodeJSONObject decodes one JSON object and returns its raw fields as well
// as the typed value. The standard json decoder intentionally treats a JSON
// null assigned to a pointer or scalar as the zero value, which makes an
// omitted field indistinguishable from an explicit null. Request handlers use
// the returned map to enforce the OpenAPI nullability and required-field
// rules before passing values to the store.
func decodeJSONObject(r *http.Request, value any) (map[string]json.RawMessage, error) {
	body, err := requestBodyData(r)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("request body must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return nil, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("multiple JSON values")
	}
	return fields, nil
}

func requestBodyData(r *http.Request) ([]byte, error) {
	if body, ok := r.Context().Value(requestBodyKey).([]byte); ok {
		if len(body) == 0 {
			return nil, io.EOF
		}
		return body, nil
	}
	if r.Body == nil {
		return nil, io.EOF
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, io.EOF
	}
	// Preserve the body for direct route callers that may inspect it after a
	// validation pass. ServeHTTP already provides an immutable context copy.
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func jsonFieldPresent(fields map[string]json.RawMessage, name string) bool {
	_, ok := fields[name]
	return ok
}

func jsonFieldNull(fields map[string]json.RawMessage, name string) bool {
	value, ok := fields[name]
	return ok && isJSONNull(value)
}

func rejectJSONNull(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		if jsonFieldNull(fields, name) {
			return taskInputError(name + " cannot be null")
		}
	}
	return nil
}

func requireJSONFields(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		value, ok := fields[name]
		if !ok {
			return taskInputError(name + " is required")
		}
		if isJSONNull(value) {
			return taskInputError(name + " cannot be null")
		}
	}
	return nil
}

// validateIdentifierArray enforces the shared Identifier item schema. In
// particular, unmarshalling []string alone would silently turn a null item
// into an empty string, and store.uniqueStrings would then discard it.
func validateIdentifierArray(raw json.RawMessage, field string, nullable bool) error {
	if isJSONNull(raw) {
		if nullable {
			return nil
		}
		return taskInputError(field + " cannot be null")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if isJSONNull(item) {
			return taskInputError(field + " items cannot be null")
		}
		var value string
		if err := json.Unmarshal(item, &value); err != nil {
			return err
		}
		if utf8.RuneCountInString(value) == 0 || utf8.RuneCountInString(value) > 200 || strings.TrimSpace(value) == "" {
			return taskInputError(field + " items must be non-empty identifiers")
		}
		if _, exists := seen[value]; exists {
			return taskInputError(field + " must not contain duplicate items")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateStringLength(raw json.RawMessage, field string, min, max int, trimEmpty bool) error {
	if isJSONNull(raw) {
		return taskInputError(field + " cannot be null")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	length := utf8.RuneCountInString(value)
	if trimEmpty && strings.TrimSpace(value) == "" {
		return taskInputError(field + " must not be empty")
	}
	if length < min || (max > 0 && length > max) {
		if min > 0 && max > 0 {
			return taskInputError(fmt.Sprintf("%s must be between %d and %d characters", field, min, max))
		}
		if min > 0 {
			return taskInputError(fmt.Sprintf("%s must contain at least %d characters", field, min))
		}
		return taskInputError(fmt.Sprintf("%s is too long", field))
	}
	return nil
}
