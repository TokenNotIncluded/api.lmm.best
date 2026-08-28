package common

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	htmlstd "html"
	"net/mail"
	"net/smtp"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func generateMessageID() (string, error) {
	sender, err := mail.ParseAddress(SMTPFrom)
	if err != nil {
		return "", fmt.Errorf("invalid SMTP sender: %w", err)
	}
	at := strings.LastIndexByte(sender.Address, '@')
	if at < 1 || at == len(sender.Address)-1 {
		return "", fmt.Errorf("invalid SMTP account")
	}
	domain := sender.Address[at+1:]
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

func parseEmailRecipients(raw string) (string, []string, error) {
	parts := strings.Split(raw, ";")
	headerAddresses := make([]string, 0, len(parts))
	recipients := make([]string, 0, len(parts))
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			return "", nil, fmt.Errorf("email recipient is empty")
		}
		address, err := mail.ParseAddress(candidate)
		if err != nil {
			return "", nil, fmt.Errorf("invalid email recipient: %w", err)
		}
		headerAddresses = append(headerAddresses, address.String())
		recipients = append(recipients, address.Address)
	}
	return strings.Join(headerAddresses, ", "), recipients, nil
}

func shouldUseSMTPLoginAuth() bool {
	if SMTPForceAuthLogin {
		return true
	}
	return isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer)
}

func getSMTPAuth() smtp.Auth {
	return AutoSMTPAuth(SMTPAccount, SMTPToken)
}

func shouldAuthenticateSMTP() bool {
	return SMTPAccount != "" && SMTPToken != ""
}

func smtpTLSConfig() *tls.Config {
	return &tls.Config{
		ServerName: SMTPServer,
		MinVersion: tls.VersionTLS12,
		// Disabled by default; SMTP_INSECURE_SKIP_VERIFY is an explicit compatibility escape hatch.
		InsecureSkipVerify: SMTPInsecureSkipVerify, // #nosec G402 -- TLS 1.2 remains mandatory.
	}
}

func newSMTPClient(addr string) (*smtp.Client, error) {
	if SMTPSSLEnabled || (SMTPPort == 465 && !SMTPStartTLSEnabled) {
		conn, err := tls.Dial("tcp", addr, smtpTLSConfig())
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, SMTPServer)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	}

	client, err := smtp.Dial(addr)
	if err != nil {
		return nil, err
	}

	if SMTPStartTLSEnabled {
		startTLSSupported, _ := client.Extension("STARTTLS")
		if !startTLSSupported {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(smtpTLSConfig()); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

	return client, nil
}

func SendEmail(subject string, receiver string, content string) error {
	if SMTPFrom == "" { // for compatibility
		SMTPFrom = SMTPAccount
	}
	sender, err := mail.ParseAddress(SMTPFrom)
	if err != nil {
		return fmt.Errorf("invalid SMTP sender: %w", err)
	}
	toHeader, recipients, err := parseEmailRecipients(receiver)
	if err != nil {
		return err
	}
	id, err2 := generateMessageID()
	if err2 != nil {
		return err2
	}
	if SMTPServer == "" && SMTPAccount == "" {
		return fmt.Errorf("SMTP 服务器未配置")
	}
	if containsEmailHeaderBreak(subject) {
		return fmt.Errorf("email subject contains a header break")
	}
	if containsEmailHeaderBreak(SystemName) {
		return fmt.Errorf("email sender name contains a header break")
	}
	safeContent, err := sanitizeEmailHTML(content)
	if err != nil {
		return fmt.Errorf("sanitize email content: %w", err)
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	fromHeader := (&mail.Address{Name: SystemName, Address: sender.Address}).String()
	message, err := buildEmailMessage(
		toHeader,
		fromHeader,
		encodedSubject,
		time.Now().Format(time.RFC1123Z),
		id,
		safeContent,
	)
	if err != nil {
		return err
	}
	auth := getSMTPAuth()
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	client, err := newSMTPClient(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if shouldAuthenticateSMTP() {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(sender.Address); err != nil {
		return err
	}
	for _, receiver := range recipients {
		if err = client.Rcpt(receiver); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(message)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	err = client.Quit()
	if err != nil {
		SysError(fmt.Sprintf("failed to send email to %s: %v", receiver, err))
	}
	return err
}

func containsEmailHeaderBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

// buildEmailMessage keeps the body as HTML while constructing each MIME header
// from a validated field. Body bytes are appended only after the mandatory
// blank line, so CRLF in a rendered body cannot create another message header.
func buildEmailMessage(toHeader, fromHeader, encodedSubject, date, messageID, content string) ([]byte, error) {
	for name, value := range map[string]string{
		"To":         toHeader,
		"From":       fromHeader,
		"Subject":    encodedSubject,
		"Date":       date,
		"Message-ID": messageID,
	} {
		if containsEmailHeaderBreak(value) {
			return nil, fmt.Errorf("email %s contains a header break", name)
		}
	}

	var message bytes.Buffer
	writeEmailHeader(&message, "To", toHeader)
	writeEmailHeader(&message, "From", fromHeader)
	writeEmailHeader(&message, "Subject", encodedSubject)
	writeEmailHeader(&message, "Date", date)
	writeEmailHeader(&message, "Message-ID", messageID)
	writeEmailHeader(&message, "Content-Type", "text/html; charset=UTF-8")
	writeEmailHeader(&message, "Content-Transfer-Encoding", "base64")
	message.WriteString("\r\n")
	writeEmailBodyBase64(&message, content)
	return message.Bytes(), nil
}

func writeEmailHeader(message *bytes.Buffer, name, value string) {
	message.WriteString(name)
	message.WriteString(": ")
	message.WriteString(value)
	message.WriteString("\r\n")
}

// writeEmailBodyBase64 keeps the MIME body opaque to the SMTP transport. In
// particular, user-controlled CRLF sequences can never become new MIME lines
// or headers after the message has been assembled. Mail clients decode the
// body back to the original UTF-8 HTML before rendering it.
func writeEmailBodyBase64(message *bytes.Buffer, content string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	for len(encoded) > 0 {
		lineLength := 76
		if len(encoded) < lineLength {
			lineLength = len(encoded)
		}
		message.WriteString(encoded[:lineLength])
		message.WriteString("\r\n")
		encoded = encoded[lineLength:]
	}
	if len(content) == 0 {
		message.WriteString("\r\n")
	}
}

var allowedEmailHTMLTags = map[string]struct{}{
	"a": {}, "blockquote": {}, "br": {}, "code": {}, "div": {}, "em": {},
	"h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {}, "hr": {},
	"li": {}, "ol": {}, "p": {}, "pre": {}, "span": {}, "strong": {},
	"ul": {},
}

var droppedEmailHTMLTags = map[string]struct{}{
	"applet": {}, "base": {}, "embed": {}, "form": {}, "iframe": {}, "input": {},
	"link": {}, "meta": {}, "object": {}, "script": {}, "style": {}, "svg": {},
	"textarea": {}, "video": {},
}

func sanitizeEmailHTML(content string) (string, error) {
	root := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(content), root)
	if err != nil {
		return "", err
	}

	var sanitized strings.Builder
	for _, node := range nodes {
		renderSanitizedEmailNode(&sanitized, node)
	}
	return sanitized.String(), nil
}

func renderSanitizedEmailNode(builder *strings.Builder, node *html.Node) {
	switch node.Type {
	case html.TextNode:
		builder.WriteString(htmlstd.EscapeString(node.Data))
	case html.ElementNode:
		tag := strings.ToLower(node.Data)
		if _, drop := droppedEmailHTMLTags[tag]; drop {
			return
		}
		if _, allowed := allowedEmailHTMLTags[tag]; !allowed {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				renderSanitizedEmailNode(builder, child)
			}
			return
		}

		builder.WriteByte('<')
		builder.WriteString(tag)
		for _, attribute := range node.Attr {
			name := strings.ToLower(strings.TrimSpace(attribute.Key))
			value := attribute.Val
			switch name {
			case "href":
				if tag != "a" {
					continue
				}
				safeValue, ok := sanitizeEmailHref(value)
				if !ok {
					continue
				}
				value = safeValue
			case "title":
				// title is safe as a plain escaped attribute.
			default:
				continue
			}
			builder.WriteByte(' ')
			builder.WriteString(name)
			builder.WriteString("=\"")
			builder.WriteString(htmlstd.EscapeString(value))
			builder.WriteByte('"')
		}
		builder.WriteByte('>')
		if tag != "br" && tag != "hr" {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				renderSanitizedEmailNode(builder, child)
			}
			builder.WriteString("</")
			builder.WriteString(tag)
			builder.WriteByte('>')
		}
	}
}

func sanitizeEmailHref(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "mailto" {
		return "", false
	}
	return value, true
}
