package tlscapture

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

const (
	connLifetime = 60 * time.Second
	plainCap     = 64 << 10
	helloCap     = 5 + 1<<14
)

type Options struct {
	ListenAddr    string
	AdvertiseHost string
}

type Receiver struct {
	ln   net.Listener
	host string
	port string
	cert tls.Certificate

	wg sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	conns   map[net.Conn]struct{}
	pending map[string]*Fingerprint
}

func Listen(opts Options) (*Receiver, error) {
	if opts.AdvertiseHost == "" {
		return nil, errors.New("tlscapture: advertise host is empty")
	}
	cert, err := selfSigned(opts.AdvertiseHost)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return nil, err
	}
	r := &Receiver{
		ln:      ln,
		host:    opts.AdvertiseHost,
		port:    strconv.Itoa(ln.Addr().(*net.TCPAddr).Port),
		cert:    cert,
		conns:   map[net.Conn]struct{}{},
		pending: map[string]*Fingerprint{},
	}
	r.wg.Add(1)
	go r.acceptLoop()
	return r, nil
}

func (r *Receiver) Close() {
	r.mu.Lock()
	r.closed = true
	conns := make([]net.Conn, 0, len(r.conns))
	for c := range r.conns {
		conns = append(conns, c)
	}
	r.mu.Unlock()
	_ = r.ln.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	r.wg.Wait()
}

func (r *Receiver) Capture(ctx context.Context, open func(ctx context.Context, url string) error) (*Fingerprint, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(buf)
	r.mu.Lock()
	r.pending[token] = nil
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, token)
		r.mu.Unlock()
	}()

	openErr := open(ctx, "https://"+net.JoinHostPort(r.host, r.port)+"/"+token)

	r.mu.Lock()
	fp := r.pending[token]
	r.mu.Unlock()
	if fp != nil {
		return fp, nil
	}
	if openErr != nil {
		return nil, fmt.Errorf("tlscapture: open: %w", openErr)
	}
	return nil, errors.New("tlscapture: no request reached the receiver")
}

func AdvertiseHost(flareURL string) (string, error) {
	u, err := url.Parse(flareURL)
	if err != nil {
		return "", err
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("tlscapture: no host in %q", flareURL)
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	// UDP connect не шлет пакетов, но ядро выбирает исходный адрес маршрута до Flare
	conn, err := net.Dial("udp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", errors.New("tlscapture: no local udp address")
	}
	return addr.IP.String(), nil
}

func (r *Receiver) acceptLoop() {
	defer r.wg.Done()
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			_ = c.Close()
			return
		}
		r.conns[c] = struct{}{}
		r.wg.Add(1)
		r.mu.Unlock()
		go r.serve(c)
	}
}

func (r *Receiver) serve(c net.Conn) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		delete(r.conns, c)
		r.mu.Unlock()
		_ = c.Close()
	}()
	_ = c.SetDeadline(time.Now().Add(connLifetime))

	raw := &helloConn{Conn: c}
	tc := tls.Server(raw, &tls.Config{
		Certificates: []tls.Certificate{r.cert},
		NextProtos:   []string{"h2"},
		MinVersion:   tls.VersionTLS12,
	})
	if err := tc.Handshake(); err != nil || tc.ConnectionState().NegotiatedProtocol != "h2" {
		return
	}
	plain := &plainConn{Conn: tc}
	hello := raw.record()
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		token := strings.TrimPrefix(req.URL.Path, "/")
		if !r.registered(token) {
			http.NotFound(w, req)
			return
		}
		fp, err := parseCapture(hello, plain.snapshot(), req.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		r.store(token, fp)
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>ok</title>")
	})
	(&http2.Server{}).ServeConn(plain, &http2.ServeConnOpts{Handler: handler})
}

func (r *Receiver) registered(token string) bool {
	if token == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.pending[token]
	return ok
}

func (r *Receiver) store(token string, fp *Fingerprint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[token]; ok {
		r.pending[token] = fp
	}
}

type helloConn struct {
	net.Conn
	mu   sync.Mutex
	buf  []byte
	done bool
}

func (c *helloConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	if !c.done && n > 0 {
		c.buf = append(c.buf, p[:n]...)
		if len(c.buf) >= 5 && (len(c.buf) >= 5+int(binary.BigEndian.Uint16(c.buf[3:5])) || len(c.buf) >= helloCap) {
			c.done = true
		}
	}
	c.mu.Unlock()
	return n, err
}

func (c *helloConn) record() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buf) < 6 || c.buf[0] != 0x16 || c.buf[5] != 0x01 {
		return nil
	}
	n := 5 + int(binary.BigEndian.Uint16(c.buf[3:5]))
	if len(c.buf) < n {
		return nil
	}
	return append([]byte(nil), c.buf[:n]...)
}

type plainConn struct {
	*tls.Conn
	mu  sync.Mutex
	buf []byte
}

func (c *plainConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		if room := plainCap - len(c.buf); room > 0 {
			c.buf = append(c.buf, p[:min(n, room)]...)
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *plainConn) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...)
}

func parseCapture(hello, plain []byte, path string) (*Fingerprint, error) {
	if hello == nil {
		return nil, errors.New("no ClientHello recorded")
	}
	if !bytes.HasPrefix(plain, []byte(http2.ClientPreface)) {
		return nil, errors.New("no HTTP/2 preface recorded")
	}
	fr := http2.NewFramer(io.Discard, bytes.NewReader(plain[len(http2.ClientPreface):]))
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)

	fp := &Fingerprint{ClientHello: hello, Headers: map[string]string{}}
	var haveSettings, haveFlow, seenHeaders bool
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return nil, fmt.Errorf("request %s not found in recorded frames: %w", path, err)
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if f.IsAck() || haveSettings {
				continue
			}
			haveSettings = true
			_ = f.ForeachSetting(func(s http2.Setting) error {
				fp.Settings = append(fp.Settings, Setting{ID: uint16(s.ID), Val: s.Val})
				return nil
			})
		case *http2.WindowUpdateFrame:
			if f.StreamID == 0 && !haveFlow && !seenHeaders {
				haveFlow = true
				fp.ConnectionFlow = f.Increment
			}
		case *http2.PriorityFrame:
			if !seenHeaders {
				fp.Priorities = append(fp.Priorities, Priority{StreamID: f.StreamID, StreamDep: f.StreamDep, Exclusive: f.Exclusive, Weight: f.Weight})
			}
		case *http2.MetaHeadersFrame:
			seenHeaders = true
			if f.PseudoValue("path") != path {
				continue
			}
			fp.StreamID = f.StreamID
			if f.HasPriority() {
				fp.HeaderPriority = &PriorityParam{StreamDep: f.Priority.StreamDep, Exclusive: f.Priority.Exclusive, Weight: f.Priority.Weight}
			}
			for _, h := range f.Fields {
				if strings.HasPrefix(h.Name, ":") {
					fp.PseudoHeaderOrder = append(fp.PseudoHeaderOrder, h.Name)
					continue
				}
				if _, dup := fp.Headers[h.Name]; dup {
					continue
				}
				fp.Headers[h.Name] = h.Value
				fp.HeaderOrder = append(fp.HeaderOrder, h.Name)
			}
			return fp, nil
		}
	}
}

func selfSigned(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "ripperr tls capture"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	} else {
		tpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
