package helper

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"

	"golang.org/x/net/http/httpproxy"
)

var proxyTransports sync.Map

var localSuffixes = []string{".local", ".lan", ".internal", ".home", ".home.arpa"}

func ProxyTransport(raw string) http.RoundTripper {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// интерфейсный nil: типизированный *http.Transport в http.Client.Transport
		// паникует вместо отката на DefaultTransport
		return nil
	}
	// NO_PROXY в ключе, потому что правила зашиваются в транспорт при сборке. Без кеша
	// каждый notify.New (а его зовут на каждом переходе предохранителя) заводил бы
	// свой пул соединений, которые никто не закрывает
	key := raw + "\x00" + noProxyEnv()
	if tr, ok := proxyTransports.Load(key); ok {
		return tr.(http.RoundTripper)
	}
	tr, _ := proxyTransports.LoadOrStore(key, buildProxyTransport(raw))
	return tr.(http.RoundTripper)
}

func buildProxyTransport(raw string) http.RoundTripper {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if u, err := url.Parse(raw); err != nil || u.Host == "" {
		// на битом адресе каждый запрос падает с ошибкой. Раз прокси задан, тихо
		// уйти напрямую с реального IP хуже
		tr.Proxy = func(*http.Request) (*url.URL, error) {
			return nil, fmt.Errorf("invalid proxy url %s", RedactURL(raw))
		}
		return tr
	}
	// http.ProxyURL гнал бы в прокси вообще все. httpproxy оставляет исключения Go
	// (localhost, loopback) и NO_PROXY, через который пользователь убирает свои локальные адреса
	proxyFunc := (&httpproxy.Config{
		HTTPProxy:  raw,
		HTTPSProxy: raw,
		NoProxy:    noProxyEnv(),
	}).ProxyFunc()
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		if isLocalTarget(req.URL.Hostname()) {
			return nil, nil
		}
		return proxyFunc(req.URL)
	}
	return tr
}

func noProxyEnv() string {
	// как в самом Go, берем первую непустую. Если склеить обе, no_proxy=* из базового
	// образа перебьет NO_PROXY пользователя и выключит прокси совсем
	if v := os.Getenv("NO_PROXY"); v != "" {
		return v
	}
	return os.Getenv("no_proxy")
}

func isLocalTarget(host string) bool {
	// httpproxy пропускает мимо только localhost и loopback. А рядом еще вебхук на домашний
	// сервер и соседи по docker-сети, гнать их через внешний прокси незачем, тем более
	// показывать оператору прокси имена раздач из тела вебхука
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	// имя без точки в публичном DNS не живет: это сосед по docker-сети или /etc/hosts
	if !strings.Contains(host, ".") {
		return true
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

func IsLocalHost(host string) bool { return isLocalTarget(host) }

func DirectTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// flaresolverr и торрент-клиент стоят рядом и через прокси не ходят. Из HTTP(S)_PROXY
	// Go исключает только localhost, имена из docker-сети нет, поэтому выключаем явно
	tr.Proxy = nil
	return tr
}
