package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/helper"
)

const (
	maxSubject  = 150
	subjectHead = "Ripperr: "
)

// не const: email_test.go подставляет сюда сертификат тестового сервера
var smtpRootCAs *x509.CertPool

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

func (n *Notifier) emailReady() bool {
	return n.cfg.EmailHost != "" && n.cfg.EmailFrom != "" && n.cfg.EmailTo != ""
}

func subjectLine(ev Event) string {
	s := ev.Subject
	if s == "" {
		s, _, _ = strings.Cut(ev.Text, "\n")
		s = strings.TrimSuffix(strings.TrimSpace(s), ":")
	}
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(subjectHead + s)
	return helper.Substr(s, 0, maxSubject)
}

func buildEmail(from *mail.Address, to []*mail.Address, ev Event) []byte {
	rcpt := make([]string, len(to))
	for i, a := range to {
		rcpt[i] = a.String()
	}
	var id [8]byte
	_, _ = rand.Read(id[:])

	var b bytes.Buffer
	hdr := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	hdr("From", from.String())
	hdr("To", strings.Join(rcpt, ", "))
	// Encode склеивает слова в одну строку, а по RFC 5322 строка заголовка не длинее 998 символов
	hdr("Subject", strings.ReplaceAll(mime.QEncoding.Encode("utf-8", subjectLine(ev)), "?= =?", "?=\r\n =?"))
	hdr("Date", time.Now().Format(time.RFC1123Z))
	hdr("Message-ID", fmt.Sprintf("<%d.%s@ripperr>", time.Now().UnixNano(), hex.EncodeToString(id[:])))
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", "text/plain; charset=utf-8")
	hdr("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")

	body := ev.plain()
	if ev.URL != "" {
		body += "\n" + ev.URL
	}
	if ev.AppLink != "" {
		body += "\n" + ev.label() + ": " + ev.AppLink
	}
	qw := quotedprintable.NewWriter(&b)
	_, _ = qw.Write([]byte(body))
	_ = qw.Close()
	return b.Bytes()
}

func (n *Notifier) sendEmail(ctx context.Context, ev Event) error {
	from, err := mail.ParseAddress(n.cfg.EmailFrom)
	if err != nil {
		return permanentError{fmt.Errorf("from: %w", err)}
	}
	to, err := mail.ParseAddressList(n.cfg.EmailTo)
	if err != nil {
		return permanentError{fmt.Errorf("to: %w", err)}
	}
	security := n.cfg.EmailSecurity
	if security == "" {
		security = "starttls"
	}
	defaultPort, ok := map[string]int{"starttls": 587, "tls": 465, "none": 25}[security]
	if !ok {
		return permanentError{fmt.Errorf("unknown email security %q", security)}
	}
	port := n.cfg.EmailPort
	if port == 0 {
		port = defaultPort
	}
	host := n.cfg.EmailHost
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: smtpRootCAs}

	var conn net.Conn
	if security == "tls" {
		conn, err = (&tls.Dialer{Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(sendTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	if security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return permanentError{errors.New("server does not advertise STARTTLS")}
		}
		if err = c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if n.cfg.EmailUsername != "" {
		if err = c.Auth(smtp.PlainAuth("", n.cfg.EmailUsername, n.cfg.EmailPassword, host)); err != nil {
			if err.Error() == "unencrypted connection" {
				return permanentError{err}
			}
			return err
		}
	}
	if err = c.Mail(from.Address); err != nil {
		return err
	}
	for _, a := range to {
		if err = c.Rcpt(a.Address); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(buildEmail(from, to, ev)); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	_ = c.Quit()
	return nil
}
