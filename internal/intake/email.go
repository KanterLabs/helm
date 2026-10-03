package intake

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/KanterLabs/helm/internal/store"
)

// MaxEmailBytes bounds a raw message; the Worker refuses larger mail first.
const MaxEmailBytes = 1 << 20

const (
	maxEmailParts       = 50
	maxEmailDepth       = 5
	maxEmailAttachments = 10
	maxEmailEvidence    = 300
)

// ErrUnreadableEmail marks mail Helm cannot turn into a ticket. Its message
// is safe to bounce to the sender.
var ErrUnreadableEmail = errors.New("unreadable email")

// EmailEnvelope is what the Worker reports alongside the raw message.
type EmailEnvelope struct {
	From string
	To   string
	// AuthResults is Cloudflare's own Authentication-Results value, when
	// the Worker saw one. It is recorded, never relied on.
	AuthResults string
	ReceivedAt  time.Time
}

// ParsedEmail is the bounded, decoded content Helm keeps from a message.
type ParsedEmail struct {
	FromAddress string
	FromDisplay string
	Subject     string
	Body        string
	MessageID   string
	Date        string
	Priority    string
	Attachments []string
	Auth        string
	// ReceiptKey identifies this message for duplicate suppression.
	ReceiptKey string
}

func unreadable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnreadableEmail, fmt.Sprintf(format, args...))
}

// ParseEmailRecipient returns the webhook tag from an envelope recipient
// of the form <localPart>+<tag>@<domain>, case-insensitively.
func ParseEmailRecipient(to, localPart, domain string) (string, bool) {
	to = strings.ToLower(strings.Trim(strings.TrimSpace(to), "<>"))
	at := strings.LastIndexByte(to, '@')
	if at < 0 || to[at+1:] != strings.ToLower(domain) {
		return "", false
	}
	tag, ok := strings.CutPrefix(to[:at], strings.ToLower(localPart)+"+")
	if !ok || len(tag) != store.EmailTagLength {
		return "", false
	}
	for _, char := range tag {
		if !(char >= 'a' && char <= 'z' || char >= '2' && char <= '7') {
			return "", false
		}
	}
	return tag, true
}

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

// ParseEmail decodes the headers and readable body of one message.
func ParseEmail(raw []byte, envelope EmailEnvelope) (ParsedEmail, error) {
	if len(raw) > MaxEmailBytes {
		return ParsedEmail{}, unreadable("message is larger than %d bytes", MaxEmailBytes)
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ParsedEmail{}, unreadable("message headers could not be read")
	}
	parser := mail.AddressParser{WordDecoder: wordDecoder}
	from, err := parser.Parse(message.Header.Get("From"))
	if err != nil || from.Address == "" {
		return ParsedEmail{}, unreadable("the message has no valid From address")
	}
	parsed := ParsedEmail{
		FromAddress: strings.ToLower(from.Address),
		FromDisplay: from.Address,
		Priority:    emailPriority(message.Header),
		Date:        bounded(oneLine(message.Header.Get("Date")), 100),
		Auth:        authSummary(envelope.AuthResults),
	}
	if name := oneLine(from.Name); name != "" {
		parsed.FromDisplay = bounded(name, 100) + " <" + from.Address + ">"
	}
	if subject, err := wordDecoder.DecodeHeader(message.Header.Get("Subject")); err == nil {
		parsed.Subject = bounded(oneLine(subject), maxGenericTitle)
	} else {
		parsed.Subject = bounded(oneLine(message.Header.Get("Subject")), maxGenericTitle)
	}
	if id := strings.Trim(strings.TrimSpace(message.Header.Get("Message-Id")), "<>"); id != "" && len(id) <= 998 && !strings.ContainsAny(id, " \r\n") {
		parsed.MessageID = id
		parsed.ReceiptKey = "message-id:" + id
	} else {
		sum := sha256.Sum256(raw)
		parsed.ReceiptKey = "raw:" + hex.EncodeToString(sum[:])
	}
	walker := &partWalker{}
	walker.walk(textproto(message.Header), message.Body, 0)
	switch {
	case walker.plain != "":
		parsed.Body = walker.plain
	case walker.html != "":
		parsed.Body = htmlToText(walker.html)
	}
	parsed.Body = truncateBody(normalizeBody(parsed.Body))
	parsed.Attachments = walker.attachments
	return parsed, nil
}

// Alert converts a parsed email for one webhook. Repeats correlate on the
// From address and normalized subject: bulk senders vary the envelope
// sender per message, so it is not used.
func (p ParsedEmail) Alert(webhookID, webhookName string) store.IntakeAlert {
	title := p.Subject
	if title == "" {
		title = bounded("Email from "+p.FromAddress, maxGenericTitle)
	}
	family := sha256.Sum256([]byte(p.FromAddress + "\n" + NormalizeEmailSubject(p.Subject)))
	key := "email:" + hex.EncodeToString(family[:16])
	evidence := map[string]string{"from": bounded(p.FromDisplay, maxEmailEvidence)}
	for name, value := range map[string]string{"date": p.Date, "message_id": p.MessageID, "authentication": p.Auth, "attachments": strings.Join(p.Attachments, ", ")} {
		if value != "" {
			evidence[name] = bounded(value, maxEmailEvidence)
		}
	}
	if p.Auth == "" {
		evidence["authentication"] = "not reported by Cloudflare"
	}
	return store.IntakeAlert{
		AlertType:             "email",
		FamilyKey:             key,
		ConditionKey:          key,
		ResourceName:          webhookName,
		ResourceID:            webhookID,
		Title:                 title,
		Description:           p.Body,
		Priority:              p.Priority,
		Evidence:              evidence,
		ReopenAfterCompletion: true,
	}
}

var replyPrefix = regexp.MustCompile(`^(?:(?:re|fwd?|aw|wg)\s*:\s*)+`)

// NormalizeEmailSubject is the subject part of the repeat family key.
func NormalizeEmailSubject(subject string) string {
	subject = strings.ToLower(strings.Join(strings.Fields(subject), " "))
	return replyPrefix.ReplaceAllString(subject, "")
}

func emailPriority(header mail.Header) string {
	if value := strings.TrimSpace(header.Get("X-Priority")); value != "" && (value[0] == '1' || value[0] == '2') {
		return "high"
	}
	if strings.EqualFold(strings.TrimSpace(header.Get("Importance")), "high") || strings.EqualFold(strings.TrimSpace(header.Get("Priority")), "urgent") {
		return "high"
	}
	return "normal"
}

var authVerdict = regexp.MustCompile(`(?i)\b(spf|dkim|dmarc)=([a-z]+)`)

func authSummary(results string) string {
	seen := map[string]bool{}
	parts := []string{}
	for _, match := range authVerdict.FindAllStringSubmatch(results, -1) {
		method := strings.ToLower(match[1])
		if !seen[method] {
			seen[method] = true
			parts = append(parts, method+"="+strings.ToLower(match[2]))
		}
	}
	return strings.Join(parts, " ")
}

type partHeader map[string][]string

func textproto(header mail.Header) partHeader { return partHeader(header) }

func (h partHeader) get(name string) string {
	for key, values := range h {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

type partWalker struct {
	parts       int
	plain       string
	html        string
	attachments []string
}

// walk visits MIME parts depth-first, keeping the first readable text/plain
// and text/html bodies and the names of attachments.
func (w *partWalker) walk(header partHeader, body io.Reader, depth int) {
	w.parts++
	if w.parts > maxEmailParts || depth > maxEmailDepth {
		return
	}
	mediaType, params, err := mime.ParseMediaType(header.get("Content-Type"))
	if err != nil {
		mediaType, params = "text/plain", map[string]string{}
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(body, params["boundary"])
		for {
			part, err := reader.NextRawPart()
			if err != nil {
				return
			}
			w.walk(partHeader(part.Header), part, depth+1)
		}
	}
	disposition, dispositionParams, _ := mime.ParseMediaType(header.get("Content-Disposition"))
	filename := dispositionParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	if disposition == "attachment" || (filename != "" && !strings.HasPrefix(mediaType, "text/")) {
		if filename != "" && len(w.attachments) < maxEmailAttachments {
			if decoded, err := wordDecoder.DecodeHeader(filename); err == nil {
				filename = decoded
			}
			w.attachments = append(w.attachments, bounded(oneLine(filename), 100))
		}
		return
	}
	if mediaType != "text/plain" && mediaType != "text/html" {
		return
	}
	if (mediaType == "text/plain" && w.plain != "") || (mediaType == "text/html" && w.html != "") {
		return
	}
	text := decodeText(decodeTransfer(header.get("Content-Transfer-Encoding"), body), params["charset"])
	if mediaType == "text/plain" {
		w.plain = text
	} else {
		w.html = text
	}
}

func decodeTransfer(encoding string, body io.Reader) []byte {
	limited := io.LimitReader(body, MaxEmailBytes)
	var data []byte
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		raw, _ := io.ReadAll(limited)
		cleaned := bytes.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, raw)
		data, _ = io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(cleaned)))
	case "quoted-printable":
		data, _ = io.ReadAll(quotedprintable.NewReader(limited))
	default:
		data, _ = io.ReadAll(limited)
	}
	return data
}

// windows1252 maps 0x80-0x9F, the only range where it differs from Latin-1.
var windows1252 = [32]rune{'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8D, 'Ž', 0x8F, 0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9D, 'ž', 'Ÿ'}

func decodeText(data []byte, charset string) string {
	switch strings.ToLower(strings.Trim(charset, `"' `)) {
	case "iso-8859-1", "latin1", "latin-1", "iso8859-1":
		return singleByte(data, false)
	case "windows-1252", "cp1252":
		return singleByte(data, true)
	default:
		return strings.ToValidUTF8(string(data), "�")
	}
}

func singleByte(data []byte, cp1252 bool) string {
	var builder strings.Builder
	for _, value := range data {
		if cp1252 && value >= 0x80 && value <= 0x9F {
			builder.WriteRune(windows1252[value-0x80])
			continue
		}
		builder.WriteRune(rune(value))
	}
	return builder.String()
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(io.LimitReader(input, 4096))
	if err != nil {
		return nil, err
	}
	return strings.NewReader(decodeText(data, charset)), nil
}

var (
	htmlDropped = regexp.MustCompile(`(?is)<(script|style|head)\b.*?</(script|style|head)\s*>`)
	htmlBreak   = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/tr|/li|/h[1-6]|/table|hr)\b[^>]*>`)
	htmlCell    = regexp.MustCompile(`(?i)<\s*/t[dh]\s*>`)
	htmlTag     = regexp.MustCompile(`(?s)<[^>]*>`)
)

// htmlToText keeps readable text only; HTML mail is never rendered.
func htmlToText(source string) string {
	text := htmlDropped.ReplaceAllString(source, "")
	text = htmlBreak.ReplaceAllString(text, "\n")
	text = htmlCell.ReplaceAllString(text, "\t")
	text = htmlTag.ReplaceAllString(text, "")
	return html.UnescapeString(text)
}

var blankRun = regexp.MustCompile(`\n{3,}`)

func normalizeBody(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRightFunc(strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, line), unicode.IsSpace)
	}
	return strings.TrimSpace(blankRun.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

func truncateBody(text string) string {
	if utf8.RuneCountInString(text) <= maxGenericDescription {
		return text
	}
	note := "\n\n[Truncated: the email was longer than 20,000 characters.]"
	runes := []rune(text)
	return string(runes[:maxGenericDescription-utf8.RuneCountInString(note)]) + note
}

func oneLine(value string) string {
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
}

func bounded(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit-1]) + "…"
}
