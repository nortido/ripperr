package notify

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSMTP struct {
	ln         net.Listener
	tlsCfg     *tls.Config
	implicit   bool
	startTLS   bool
	authReply  string
	mailReply  string
	mu         sync.Mutex
	cmds       []string
	data       string
	conns      int
	authOverTL []bool
}

func testTLSMaterial(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	s := httptest.NewUnstartedServer(http.NotFoundHandler())
	s.StartTLS()
	defer s.Close()
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	return &tls.Config{Certificates: s.TLS.Certificates, MinVersion: tls.VersionTLS12}, pool
}

func newFakeSMTP(t *testing.T, f *fakeSMTP) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if f.implicit {
		ln = tls.NewListener(ln, f.tlsCfg)
	}
	f.ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns++
			f.mu.Unlock()
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int {
	return f.ln.Addr().(*net.TCPAddr).Port
}

func (f *fakeSMTP) record(s string) {
	f.mu.Lock()
	f.cmds = append(f.cmds, s)
	f.mu.Unlock()
}

func (f *fakeSMTP) seen(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	_, isTLS := c.(*tls.Conn)
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.record(line)
		switch {
		case strings.HasPrefix(line, "EHLO"):
			w("250-fake")
			w("250-AUTH PLAIN")
			if f.startTLS && !isTLS {
				w("250-STARTTLS")
			}
			w("250 8BITMIME")
		case line == "STARTTLS":
			w("220 go ahead")
			tc := tls.Server(c, f.tlsCfg)
			if err := tc.Handshake(); err != nil {
				return
			}
			c, isTLS = tc, true
			r = bufio.NewReader(c)
			w = func(s string) { _, _ = io.WriteString(tc, s+"\r\n") }
		case strings.HasPrefix(line, "AUTH"):
			f.mu.Lock()
			f.authOverTL = append(f.authOverTL, isTLS)
			f.mu.Unlock()
			if f.authReply != "" {
				w(f.authReply)
			} else {
				w("235 ok")
			}
		case strings.HasPrefix(line, "MAIL FROM"):
			if f.mailReply != "" {
				w(f.mailReply)
			} else {
				w("250 ok")
			}
		case strings.HasPrefix(line, "RCPT TO"):
			w("250 ok")
		case line == "DATA":
			w("354 go")
			tp := textproto.NewReader(r)
			b, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.data = string(b)
			f.mu.Unlock()
			w("250 queued")
		case line == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func emailCfg(f *fakeSMTP, security string) Config {
	return Config{
		Enabled:       true,
		EmailHost:     "127.0.0.1",
		EmailPort:     f.port(),
		EmailSecurity: security,
		EmailUsername: "user",
		EmailPassword: "s3cret",
		EmailFrom:     "Ripperr <bot@example.com>",
		EmailTo:       "a@example.com, b@example.com",
	}
}

func TestEmailMessage(t *testing.T) {
	from := &mail.Address{Name: "Ripperr", Address: "bot@example.com"}
	to := []*mail.Address{{Address: "a@example.com"}, {Address: "b@example.com"}}
	ev := Event{Kind: KindUpdated, Text: "Раздача обновлена:", Subject: "Релиз.2026\r\nBcc: x@evil.com", URL: "https://example.com/t1"}
	raw := buildEmail(from, to, ev)

	if strings.Contains(strings.ReplaceAll(string(raw), "\r\n", ""), "\n") {
		t.Fatalf("bare LF in message: %q", raw)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatalf("subject injected a header: %q", raw)
	}
	subj, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(subj, "Ripperr: Релиз.2026") || strings.ContainsAny(subj, "\r\n") {
		t.Fatalf("subject = %q", subj)
	}
	if strings.Contains(msg.Header.Get("Subject"), "Релиз") {
		t.Fatalf("subject not encoded: %q", msg.Header.Get("Subject"))
	}
	if got := msg.Header.Get("To"); !strings.Contains(got, "a@example.com") || !strings.Contains(got, "b@example.com") {
		t.Fatalf("To = %q", got)
	}
	if got := msg.Header.Get("From"); !strings.Contains(got, "bot@example.com") {
		t.Fatalf("From = %q", got)
	}
	if _, err := msg.Header.Date(); err != nil {
		t.Fatalf("Date: %v", err)
	}
	if !regexp.MustCompile(`^<\d+\.[0-9a-f]+@ripperr>$`).MatchString(msg.Header.Get("Message-Id")) {
		t.Fatalf("Message-ID = %q", msg.Header.Get("Message-Id"))
	}
	if msg.Header.Get("Mime-Version") != "1.0" ||
		msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" ||
		!strings.Contains(msg.Header.Get("Content-Type"), "charset=utf-8") {
		t.Fatalf("mime headers: %v", msg.Header)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	want := "Раздача обновлена:\nРелиз.2026\r\nBcc: x@evil.com\nhttps://example.com/t1"
	if strings.ReplaceAll(string(body), "\r\n", "\n") != strings.ReplaceAll(want, "\r\n", "\n") {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestEmailSubjectLine(t *testing.T) {
	long := strings.Repeat("я", 400)
	cases := []struct {
		ev   Event
		want string
	}{
		{Event{Text: "Раздача обновлена:", Subject: "Релиз"}, "Ripperr: Релиз"},
		{TrackerUpEvent("rutracker"), "Ripperr: Tracker rutracker is available again, checks resumed."},
		{Event{Text: "Ошибка:\nвторая строка"}, "Ripperr: Ошибка"},
		{Event{Text: "x", Subject: long}, "Ripperr: " + strings.Repeat("я", 141)},
	}
	for _, c := range cases {
		if got := subjectLine(c.ev); got != c.want {
			t.Errorf("subjectLine(%+v) = %q, want %q", c.ev, got, c.want)
		}
	}
}

func TestSendEmailStartTLSRequired(t *testing.T) {
	f := newFakeSMTP(t, &fakeSMTP{})
	n := New(emailCfg(f, "starttls"), "")
	err := n.sendEmail(context.Background(), UpdatedEvent("x", "", ""))
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("err = %v, want STARTTLS error", err)
	}
	if f.seen("AUTH")+f.seen("MAIL")+f.seen("DATA") != 0 {
		t.Fatalf("session continued without TLS: %v", f.cmds)
	}
	n = New(emailCfg(f, ""), "")
	if err := n.sendEmail(context.Background(), UpdatedEvent("x", "", "")); err == nil {
		t.Fatal("empty security must behave like starttls")
	}
}

func TestSendEmailNoneMode(t *testing.T) {
	f := newFakeSMTP(t, &fakeSMTP{})
	n := New(emailCfg(f, "none"), "")
	if err := n.sendEmail(context.Background(), UpdatedEvent("Релиз", "https://e.com/1", "")); err != nil {
		t.Fatal(err)
	}
	if f.seen("MAIL FROM:<bot@example.com>") != 1 || f.seen("RCPT TO:<a@example.com>") != 1 || f.seen("RCPT TO:<b@example.com>") != 1 || f.seen("DATA") != 1 {
		t.Fatalf("commands: %v", f.cmds)
	}
	if f.seen("AUTH PLAIN") != 1 {
		t.Fatalf("auth not sent: %v", f.cmds)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(f.data, "Subject: ") || !strings.Contains(f.data, "Message-Id: ") && !strings.Contains(f.data, "Message-ID: ") {
		t.Fatalf("data = %q", f.data)
	}
}

func TestSendEmailNoAuthWithoutUsername(t *testing.T) {
	f := newFakeSMTP(t, &fakeSMTP{})
	cfg := emailCfg(f, "none")
	cfg.EmailUsername, cfg.EmailPassword = "", ""
	if err := New(cfg, "").sendEmail(context.Background(), UpdatedEvent("x", "", "")); err != nil {
		t.Fatal(err)
	}
	if f.seen("AUTH") != 0 {
		t.Fatalf("auth sent without username: %v", f.cmds)
	}
}

func TestSendEmailStartTLS(t *testing.T) {
	cfg, pool := testTLSMaterial(t)
	orig := smtpRootCAs
	smtpRootCAs = pool
	defer func() { smtpRootCAs = orig }()

	f := newFakeSMTP(t, &fakeSMTP{startTLS: true, tlsCfg: cfg})
	n := New(emailCfg(f, "starttls"), "")
	if err := n.sendEmail(context.Background(), UpdatedEvent("x", "", "")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.authOverTL) != 1 || !f.authOverTL[0] {
		t.Fatalf("AUTH must come after STARTTLS: %v", f.authOverTL)
	}
	if f.data == "" {
		t.Fatal("message not delivered")
	}
}

func TestSendEmailImplicitTLS(t *testing.T) {
	cfg, pool := testTLSMaterial(t)
	orig := smtpRootCAs
	smtpRootCAs = pool
	defer func() { smtpRootCAs = orig }()

	f := newFakeSMTP(t, &fakeSMTP{implicit: true, tlsCfg: cfg})
	n := New(emailCfg(f, "tls"), "")
	if err := n.sendEmail(context.Background(), UpdatedEvent("x", "", "")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	over, data := f.authOverTL, f.data
	f.mu.Unlock()
	if len(over) != 1 || !over[0] || data == "" {
		t.Fatalf("tls session: auth %v data %q", over, data)
	}

	smtpRootCAs = nil
	if err := n.sendEmail(context.Background(), UpdatedEvent("x", "", "")); err == nil {
		t.Fatal("untrusted certificate must fail")
	}
}

func TestEmailRetryClassification(t *testing.T) {
	f := newFakeSMTP(t, &fakeSMTP{authReply: "535 5.7.8 bad credentials"})
	n := New(emailCfg(f, "none"), "")
	err := n.sendEmail(context.Background(), UpdatedEvent("x", "", ""))
	var te *textproto.Error
	if !errors.As(err, &te) || te.Code != 535 {
		t.Fatalf("err = %v, want textproto 535", err)
	}
	if isRetryable(err) {
		t.Error("535 must not be retried")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("password leaked: %v", err)
	}

	f = newFakeSMTP(t, &fakeSMTP{mailReply: "421 try later"})
	err = New(emailCfg(f, "none"), "").sendEmail(context.Background(), UpdatedEvent("x", "", ""))
	if err == nil || !isRetryable(err) {
		t.Errorf("421 must be retried: %v", err)
	}
}

func TestEmailReadyAndTransport(t *testing.T) {
	cfg := Config{EmailHost: "h", EmailFrom: "a@b.c"}
	if New(cfg, "").HasTransport() {
		t.Error("email without recipients is not a transport")
	}
	cfg.EmailTo = "x@y.z"
	if !New(cfg, "").HasTransport() {
		t.Error("host+from+to is a transport")
	}
}

func TestNotifyRoutesEmailByEvents(t *testing.T) {
	f := newFakeSMTP(t, &fakeSMTP{})
	cfg := emailCfg(f, "none")
	cfg.EmailEvents = []string{}
	New(cfg, "").Notify(TrackerDownEvent("rutracker", "boom"))
	time.Sleep(150 * time.Millisecond)
	f.mu.Lock()
	got := f.conns
	f.mu.Unlock()
	if got != 0 {
		t.Fatalf("silent email channel connected %d times", got)
	}

	cfg.EmailEvents = nil
	New(cfg, "").Notify(TrackerDownEvent("rutracker", "boom"))
	deadline := time.Now().Add(2 * time.Second)
	for f.seen("DATA") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("default events must reach email channel")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEmailLongSubjectFolded(t *testing.T) {
	from := &mail.Address{Address: "bot@example.com"}
	to := []*mail.Address{{Address: "a@example.com"}}
	ev := Event{Kind: KindUpdated, Text: "x", Subject: strings.Repeat("Релиз", 40)}
	raw := buildEmail(from, to, ev)
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if len(strings.TrimPrefix(line, "Subject: ")) > 75 || len(line) > 998 {
			t.Errorf("header line %d bytes: %q", len(line), line)
		}
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	got, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if want := subjectLine(ev); got != want {
		t.Fatalf("decoded subject = %q, want %q", got, want)
	}
}

func TestSendEmailRejectsUnknownSecurity(t *testing.T) {
	f := newFakeSMTP(t, &fakeSMTP{})
	err := New(emailCfg(f, "STARTTLS"), "").sendEmail(context.Background(), UpdatedEvent("x", "", ""))
	if err == nil || !strings.Contains(err.Error(), "security") {
		t.Fatalf("err = %v, want security error", err)
	}
	if isRetryable(err) {
		t.Error("config error must not be retried")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conns != 0 {
		t.Fatalf("connected %d times despite bad security", f.conns)
	}
}

func TestEmailConfigErrorsNotRetried(t *testing.T) {
	f := newFakeSMTP(t, &fakeSMTP{})
	err := New(emailCfg(f, "starttls"), "").sendEmail(context.Background(), UpdatedEvent("x", "", ""))
	if err == nil || isRetryable(err) {
		t.Errorf("missing STARTTLS must not be retried: %v", err)
	}
	cfg := emailCfg(f, "none")
	cfg.EmailHost = "0.0.0.0"
	err = New(cfg, "").sendEmail(context.Background(), UpdatedEvent("x", "", ""))
	if err == nil || !strings.Contains(err.Error(), "unencrypted") || isRetryable(err) {
		t.Errorf("PlainAuth refusal must not be retried: %v", err)
	}
	cfg = emailCfg(f, "none")
	cfg.EmailFrom = "not an address"
	err = New(cfg, "").sendEmail(context.Background(), UpdatedEvent("x", "", ""))
	if err == nil || isRetryable(err) {
		t.Errorf("bad from must not be retried: %v", err)
	}
}
