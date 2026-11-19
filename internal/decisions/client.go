package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

const endpoint = "https://api.openai.com/v1/decisions"
const requestTimeout = 6 * time.Second

type choice struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

type question struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Instructions string   `json:"instructions"`
	Choices      []choice `json:"choices"`
}

func choose(name, instructions string, choices ...choice) question {
	return question{Type: "choice", Name: name, Instructions: instructions, Choices: choices}
}

func (q question) accepts(value string) bool {
	for _, candidate := range q.Choices {
		if candidate.Value == value {
			return true
		}
	}
	return false
}

type client struct {
	url  string
	http *http.Client
}

func newClient(publicOrigin, testOrigin string) client {
	target := endpoint
	// The browser fixture runs both services on IPv4 loopback. Public installs
	// always send credentials directly to OpenAI.
	if isLoopbackOrigin(publicOrigin) && isLoopbackOrigin(testOrigin) {
		target = testOrigin + "/v1/decisions"
	}
	return client{url: target, http: &http.Client{
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func isLoopbackOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" &&
		u.User == nil && raw == "http://"+u.Host
}

var (
	ErrKeyRejected   = errors.New("OpenAI rejected the API key")
	ErrAccessDenied  = errors.New("OpenAI denied access to Decisions")
	ErrUsageLimit    = errors.New("OpenAI usage limit reached")
	ErrUnavailable   = errors.New("OpenAI Decisions is unavailable")
	ErrInvalidAnswer = errors.New("OpenAI returned an invalid triage answer")
)

type taskEvidence struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type contentPart struct {
	Type string       `json:"type"`
	Data taskEvidence `json:"data"`
}

type inputMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

func (c client) ask(ctx context.Context, key string, evidence taskEvidence, questions []question) (map[string]string, error) {
	payload, err := json.Marshal(struct {
		Model     string         `json:"model"`
		Input     []inputMessage `json:"input"`
		Questions []question     `json:"questions"`
	}{
		Model:     "gpt-6-luna",
		Input:     []inputMessage{{Role: "user", Content: []contentPart{{Type: "input_json", Data: evidence}}}},
		Questions: questions,
	})
	if err != nil {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, ErrKeyRejected
	case http.StatusForbidden:
		return nil, ErrAccessDenied
	case http.StatusTooManyRequests:
		return nil, ErrUsageLimit
	default:
		return nil, ErrUnavailable
	}
	const responseLimit = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	var packet struct {
		Answers []struct {
			Type   string `json:"type"`
			Name   string `json:"name"`
			Choice string `json:"choice"`
		} `json:"answers"`
	}
	if len(body) > responseLimit || json.Unmarshal(body, &packet) != nil || len(packet.Answers) != len(questions) {
		return nil, ErrInvalidAnswer
	}
	answers := make(map[string]string, len(questions))
	for _, answer := range packet.Answers {
		if answer.Type != "choice" {
			return nil, ErrInvalidAnswer
		}
		answers[answer.Name] = answer.Choice
	}
	for _, q := range questions {
		if !q.accepts(answers[q.Name]) {
			return nil, ErrInvalidAnswer
		}
	}
	return answers, nil
}
