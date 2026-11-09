package mailer

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

type Sender struct {
	Name    string
	Address string
}

type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
	ContentID   string
}

type Message struct {
	To          string
	From        Sender
	ReplyTo     string
	Subject     string
	HTML        string
	Text        string
	Attachments []Attachment
}

func SanitizeHeaderValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func ValidateAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("address is empty")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return "", fmt.Errorf("invalid address: %w", err)
	}
	if strings.ContainsAny(addr.Address, "\r\n\x00") {
		return "", errors.New("invalid address: control characters")
	}
	return addr.Address, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

func qpBody(s string) string {
	var out bytes.Buffer
	w := quotedprintable.NewWriter(&out)
	if _, err := w.Write([]byte(s)); err != nil {
		return s
	}
	if err := w.Close(); err != nil {
		return s
	}
	return out.String()
}

func writeTextPart(buf *bytes.Buffer, contentType, body string) {
	writeHeader(buf, "Content-Type", contentType+"; charset=UTF-8")
	writeHeader(buf, "Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")
	buf.WriteString(qpBody(body))
	if !strings.HasSuffix(buf.String(), "\r\n") {
		buf.WriteString("\r\n")
	}
}

func writeBase64(buf *bytes.Buffer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		buf.WriteString(enc[:76])
		buf.WriteString("\r\n")
		enc = enc[76:]
	}
	buf.WriteString(enc)
	buf.WriteString("\r\n")
}

func cleanFilename(name string) string {
	name = SanitizeHeaderValue(name)
	name = strings.NewReplacer("/", "_", "\\", "_", "\"", "_").Replace(name)
	if name == "" {
		return "attachment"
	}
	return name
}

func writeBody(buf *bytes.Buffer, m Message) error {
	switch {
	case m.HTML != "" && m.Text != "":
		boundary, err := randomHex(16)
		if err != nil {
			return err
		}
		writeHeader(buf, "Content-Type", "multipart/alternative; boundary="+boundary)
		buf.WriteString("\r\n--" + boundary + "\r\n")
		writeTextPart(buf, "text/plain", m.Text)
		buf.WriteString("--" + boundary + "\r\n")
		writeTextPart(buf, "text/html", m.HTML)
		buf.WriteString("--" + boundary + "--\r\n")
	case m.HTML != "":
		writeTextPart(buf, "text/html", m.HTML)
	default:
		writeTextPart(buf, "text/plain", m.Text)
	}
	return nil
}

func writeBodyWithInline(buf *bytes.Buffer, m Message, inline []Attachment) error {
	if len(inline) == 0 {
		return writeBody(buf, m)
	}
	boundary, err := randomHex(16)
	if err != nil {
		return err
	}
	writeHeader(buf, "Content-Type", "multipart/related; boundary="+boundary)
	buf.WriteString("\r\n--" + boundary + "\r\n")
	if err := writeBody(buf, m); err != nil {
		return err
	}
	for _, a := range inline {
		if len(a.Content) == 0 {
			return fmt.Errorf("inline attachment %q is empty", a.Filename)
		}
		ctype := "application/octet-stream"
		if mt, _, perr := mime.ParseMediaType(SanitizeHeaderValue(a.ContentType)); perr == nil && mt != "" {
			ctype = mt
		}
		name := cleanFilename(a.Filename)
		buf.WriteString("--" + boundary + "\r\n")
		writeHeader(buf, "Content-Type", mime.FormatMediaType(ctype, map[string]string{"name": name}))
		writeHeader(buf, "Content-ID", "<"+SanitizeHeaderValue(a.ContentID)+">")
		writeHeader(buf, "Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
		writeHeader(buf, "Content-Transfer-Encoding", "base64")
		buf.WriteString("\r\n")
		writeBase64(buf, a.Content)
	}
	buf.WriteString("--" + boundary + "--\r\n")
	return nil
}

func (m Message) Build(now time.Time) (raw []byte, envelopeFrom, envelopeTo string, err error) {
	to, err := ValidateAddress(m.To)
	if err != nil {
		return nil, "", "", invalid("recipient", err)
	}
	from, err := ValidateAddress(m.From.Address)
	if err != nil {
		return nil, "", "", invalid("sender", err)
	}
	if strings.TrimSpace(m.Subject) == "" {
		return nil, "", "", invalid("subject", errors.New("subject is empty"))
	}
	if m.HTML == "" && m.Text == "" {
		return nil, "", "", invalid("body", errors.New("body is empty"))
	}

	fromHeader := (&mail.Address{Name: SanitizeHeaderValue(m.From.Name), Address: from}).String()
	domain := from[strings.LastIndex(from, "@")+1:]
	idPart, err := randomHex(16)
	if err != nil {
		return nil, "", "", fail("message-id", err)
	}

	var buf bytes.Buffer
	writeHeader(&buf, "Date", now.Format(time.RFC1123Z))
	writeHeader(&buf, "From", fromHeader)
	writeHeader(&buf, "To", (&mail.Address{Address: to}).String())
	if strings.TrimSpace(m.ReplyTo) != "" {
		reply, rerr := ValidateAddress(m.ReplyTo)
		if rerr != nil {
			return nil, "", "", invalid("reply-to", rerr)
		}
		writeHeader(&buf, "Reply-To", (&mail.Address{Address: reply}).String())
	}
	writeHeader(&buf, "Subject", mime.QEncoding.Encode("utf-8", SanitizeHeaderValue(m.Subject)))
	writeHeader(&buf, "Message-ID", "<"+idPart+"@"+domain+">")
	writeHeader(&buf, "MIME-Version", "1.0")

	var inline, regular []Attachment
	for _, a := range m.Attachments {
		if strings.TrimSpace(a.ContentID) != "" {
			inline = append(inline, a)
		} else {
			regular = append(regular, a)
		}
	}

	if len(regular) == 0 {
		if err := writeBodyWithInline(&buf, m, inline); err != nil {
			return nil, "", "", fail("body", err)
		}
		return buf.Bytes(), from, to, nil
	}

	boundary, err := randomHex(16)
	if err != nil {
		return nil, "", "", fail("boundary", err)
	}
	writeHeader(&buf, "Content-Type", "multipart/mixed; boundary="+boundary)
	buf.WriteString("\r\n--" + boundary + "\r\n")
	if err := writeBodyWithInline(&buf, m, inline); err != nil {
		return nil, "", "", fail("body", err)
	}
	for _, a := range regular {
		if len(a.Content) == 0 {
			return nil, "", "", invalid("attachment", fmt.Errorf("attachment %q is empty", a.Filename))
		}
		name := cleanFilename(a.Filename)
		ctype := "application/octet-stream"
		if mt, _, perr := mime.ParseMediaType(SanitizeHeaderValue(a.ContentType)); perr == nil && mt != "" {
			ctype = mt
		}
		buf.WriteString("--" + boundary + "\r\n")
		if v := mime.FormatMediaType(ctype, map[string]string{"name": name}); v != "" {
			writeHeader(&buf, "Content-Type", v)
		} else {
			writeHeader(&buf, "Content-Type", ctype)
		}
		if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
			writeHeader(&buf, "Content-Disposition", v)
		} else {
			writeHeader(&buf, "Content-Disposition", "attachment")
		}
		writeHeader(&buf, "Content-Transfer-Encoding", "base64")
		buf.WriteString("\r\n")
		writeBase64(&buf, a.Content)
	}
	buf.WriteString("--" + boundary + "--\r\n")
	return buf.Bytes(), from, to, nil
}
