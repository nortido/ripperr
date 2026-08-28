package helper

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// возвращает интерфейсный nil, а не типизированный *http.Transport: присвоенный
// в http.Client.Transport типизированный nil паникует вместо отката на DefaultTransport
func ProxyTransport(raw string) http.RoundTripper {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if u, err := url.Parse(raw); err != nil || u.Host == "" {
		return nil
	}
	// через httpproxy, а не через http.ProxyURL напрямую: тот отправляет в прокси
	// вообще всё, а здесь остаются исключения самого Go (localhost, loopback) и
	// NO_PROXY, которым пользователь выводит из-под прокси свои локальные адреса
	// (вебхук на домашний сервер, зеркало трекера в той же сети)
	proxyFunc := (&httpproxy.Config{
		HTTPProxy:  raw,
		HTTPSProxy: raw,
		NoProxy:    os.Getenv("NO_PROXY") + "," + os.Getenv("no_proxy"),
	}).ProxyFunc()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = func(req *http.Request) (*url.URL, error) { return proxyFunc(req.URL) }
	return tr
}

// локальные сервисы (flaresolverr, торрент-клиент) стоят рядом с приложением и через
// прокси не ходят: из HTTP(S)_PROXY Go исключает только localhost, но не имена хостов
// в docker-сети, поэтому прямое соединение задаём явно, а не надеемся на пустое окружение
func DirectTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return tr
}
