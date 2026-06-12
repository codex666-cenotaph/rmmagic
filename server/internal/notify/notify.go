// Package notify delivers alert notifications: plain-text email over
// SMTP and JSON webhooks signed with a per-channel HMAC secret.
// Delivery state (retries, outbox) lives in the worker; this package
// only knows how to send one message.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig is server-wide email transport configuration (dev:
// mailpit on localhost:1025). User/Pass empty disables AUTH; net/smtp
// negotiates STARTTLS when the server offers it.
type SMTPConfig struct {
	Addr string // host:port; empty disables email delivery
	From string
	User string
	Pass string
}

type Notifier struct {
	SMTP SMTPConfig
	HTTP *http.Client
}

func New(smtpCfg SMTPConfig) *Notifier {
	return &Notifier{
		SMTP: smtpCfg,
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
			// Don't follow redirects: a redirect would re-send the signed
			// body to a location the operator never configured.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// SendEmail sends one plain-text message to the recipients.
func (n *Notifier) SendEmail(to []string, subject, body string) error {
	if n.SMTP.Addr == "" {
		return errors.New("smtp not configured (RMM_SMTP_ADDR)")
	}
	if len(to) == 0 {
		return errors.New("no recipients")
	}
	for _, rcpt := range to {
		// CRLF in an address would smuggle extra headers/commands.
		if strings.ContainsAny(rcpt, "\r\n") {
			return fmt.Errorf("invalid recipient")
		}
	}
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", n.SMTP.From)
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&msg, "Subject: %s\r\n", sanitizeHeader(subject))
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	msg.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))

	var auth smtp.Auth
	if n.SMTP.User != "" {
		host := n.SMTP.Addr
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		auth = smtp.PlainAuth("", n.SMTP.User, n.SMTP.Pass, host)
	}
	return smtp.SendMail(n.SMTP.Addr, auth, n.SMTP.From, to, msg.Bytes())
}

// Sign computes the webhook signature: HMAC-SHA256 over
// "<unix-ts>.<body>" so receivers can reject replays outside their
// freshness window.
func Sign(secret []byte, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature is the receiver-side check (used by tests and
// documented for integrators).
func VerifySignature(secret []byte, ts int64, body []byte, signatureHex string) bool {
	want, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(Sign(secret, ts, body))
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
}

// SendWebhook POSTs the JSON body with X-RMM-Timestamp and
// X-RMM-Signature headers. Any non-2xx response is an error.
func (n *Notifier) SendWebhook(ctx context.Context, url string, secret, body []byte) error {
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "rmmagic-webhook/1")
	req.Header.Set("X-RMM-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-RMM-Signature", Sign(secret, ts, body))

	resp, err := n.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}
