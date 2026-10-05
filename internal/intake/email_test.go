package intake

import (
	"errors"
	"strings"
	"testing"
)

func crlf(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n")) }

func TestParseEmailPlainText(t *testing.T) {
	parsed, err := ParseEmail(crlf(
		"From: Backup Bot <Backup@Example.com>",
		"To: helm-alerts+abcdefghijklmnop@example.com",
		"Subject: Nightly backup failed",
		"Message-ID: <abc123@mail.example.com>",
		"Date: Sat, 3 Oct 2026 12:00:00 +0000",
		"X-Priority: 1 (Highest)",
		"",
		"Disk /dev/sda1 is full.\t",
		"",
		"",
		"",
		"Retry tomorrow.",
	), EmailEnvelope{AuthResults: "mx.cloudflare.net; dkim=pass header.d=example.com; spf=pass smtp.mailfrom=example.com; dmarc=pass"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.FromAddress != "backup@example.com" || parsed.FromDisplay != "Backup Bot <Backup@Example.com>" {
		t.Fatalf("from = %q / %q", parsed.FromAddress, parsed.FromDisplay)
	}
	if parsed.Subject != "Nightly backup failed" || parsed.Priority != "high" {
		t.Fatalf("subject/priority = %q / %q", parsed.Subject, parsed.Priority)
	}
	if parsed.Body != "Disk /dev/sda1 is full.\n\nRetry tomorrow." {
		t.Fatalf("body = %q", parsed.Body)
	}
	if parsed.ReceiptKey != "message-id:abc123@mail.example.com" || parsed.Auth != "dkim=pass spf=pass dmarc=pass" {
		t.Fatalf("receipt/auth = %q / %q", parsed.ReceiptKey, parsed.Auth)
	}
	alert := parsed.Alert("hook-1", "Backups")
	if alert.Title != "Nightly backup failed" || alert.Priority != "high" || !alert.ReopenAfterCompletion || alert.Evidence["from"] != "Backup Bot <Backup@Example.com>" {
		t.Fatalf("alert = %+v", alert)
	}
}

func TestParseEmailMultipartPrefersPlainAndListsAttachments(t *testing.T) {
	parsed, err := ParseEmail(crlf(
		"From: alerts@example.com",
		"Subject: =?UTF-8?B?w4RsYXJtOiBDUFUgw7xiZXIgOTAl?=",
		`Content-Type: multipart/mixed; boundary="outer"`,
		"",
		"--outer",
		`Content-Type: multipart/alternative; boundary="inner"`,
		"",
		"--inner",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>HTML version</p>",
		"--inner",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"CPU =C3=BCber 90% auf host-1=",
		" seit 5 Minuten.",
		"--inner--",
		"--outer",
		`Content-Type: application/pdf; name="report.pdf"`,
		"Content-Disposition: attachment; filename=\"report.pdf\"",
		"Content-Transfer-Encoding: base64",
		"",
		"JVBERi0xLjQK",
		"--outer--",
	), EmailEnvelope{})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Subject != "Älarm: CPU über 90%" {
		t.Fatalf("subject = %q", parsed.Subject)
	}
	if parsed.Body != "CPU über 90% auf host-1 seit 5 Minuten." {
		t.Fatalf("body = %q", parsed.Body)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0] != "report.pdf" {
		t.Fatalf("attachments = %v", parsed.Attachments)
	}
	if !strings.HasPrefix(parsed.ReceiptKey, "raw:") {
		t.Fatalf("a message without Message-ID must use a raw digest, got %q", parsed.ReceiptKey)
	}
	if got := parsed.Alert("h", "n").Evidence["authentication"]; got != "not reported by Cloudflare" {
		t.Fatalf("authentication evidence = %q", got)
	}
}

func TestParseEmailHTMLOnlyBase64Latin1(t *testing.T) {
	parsed, err := ParseEmail(crlf(
		"From: nas@example.com",
		"Subject: Volume degraded",
		"Content-Type: text/html; charset=iso-8859-1",
		"Content-Transfer-Encoding: base64",
		"",
		// "<style>x{}</style><p>Volume&nbsp;1 d\xe9grad\xe9</p><br><script>alert(1)</script>Check disk 2"
		"PHN0eWxlPnh7fTwvc3R5bGU+PHA+Vm9sdW1lJm5ic3A7MSBk6Wdy",
		"YWTpPC9wPjxicj48c2NyaXB0PmFsZXJ0KDEpPC9zY3JpcHQ+Q2hl",
		"Y2sgZGlzayAy",
	), EmailEnvelope{})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Body != "Volume 1 dégradé\n\nCheck disk 2" {
		t.Fatalf("body = %q", parsed.Body)
	}
}

func TestParseEmailRejectsMissingFromAndOversize(t *testing.T) {
	if _, err := ParseEmail(crlf("Subject: hi", "", "body"), EmailEnvelope{}); !errors.Is(err, ErrUnreadableEmail) {
		t.Fatalf("missing From: err = %v", err)
	}
	if _, err := ParseEmail(make([]byte, MaxEmailBytes+1), EmailEnvelope{}); !errors.Is(err, ErrUnreadableEmail) {
		t.Fatalf("oversize: err = %v", err)
	}
}

func TestEmptySubjectAndTruncation(t *testing.T) {
	parsed, err := ParseEmail(crlf("From: cron@example.com", "", strings.Repeat("x", 25000)), EmailEnvelope{})
	if err != nil {
		t.Fatal(err)
	}
	alert := parsed.Alert("h", "n")
	if alert.Title != "Email from cron@example.com" {
		t.Fatalf("title = %q", alert.Title)
	}
	if len([]rune(alert.Description)) != maxGenericDescription || !strings.HasSuffix(alert.Description, "longer than 20,000 characters.]") {
		t.Fatalf("description length %d", len([]rune(alert.Description)))
	}
}

func TestRepeatFamilyIgnoresReplyPrefixesAndCase(t *testing.T) {
	first, _ := ParseEmail(crlf("From: a@example.com", "Subject: Disk  FULL on host-1", "", "x"), EmailEnvelope{})
	second, _ := ParseEmail(crlf("From: A@Example.com", "Subject: Re: Fwd: disk full on host-1", "", "y"), EmailEnvelope{})
	other, _ := ParseEmail(crlf("From: b@example.com", "Subject: Disk FULL on host-1", "", "x"), EmailEnvelope{})
	if first.Alert("h", "n").FamilyKey != second.Alert("h", "n").FamilyKey {
		t.Fatal("same sender and subject must share a family")
	}
	if first.Alert("h", "n").FamilyKey == other.Alert("h", "n").FamilyKey {
		t.Fatal("a different sender must not share a family")
	}
}

func TestParseEmailRecipient(t *testing.T) {
	cases := []struct {
		to   string
		tag  string
		want bool
	}{
		{"helm-alerts+backups-k3j9x2@example.com", "backups-k3j9x2", true},
		{"<Helm-Alerts+Backups-K3J9X2@Example.COM>", "backups-k3j9x2", true},
		{"helm-alerts@example.com", "", false},
		{"helm-alerts+ab@example.com", "", false},
		{"helm-alerts+-bad-tag@example.com", "", false},
		{"helm-alerts+has.dot@example.com", "", false},
		{"other+backups-k3j9x2@example.com", "", false},
		{"helm-alerts+backups-k3j9x2@example.org", "", false},
	}
	for _, item := range cases {
		tag, ok := ParseEmailRecipient(item.to, "helm-alerts", "example.com")
		if ok != item.want || tag != item.tag {
			t.Errorf("%s: got %q %v", item.to, tag, ok)
		}
	}
}
