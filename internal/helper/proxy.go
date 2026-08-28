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

// адрес плюс NO_PROXY: правила зашиваются в транспорт на момент сборки, а notify.New
// зовут на каждый переход предохранителя и на каждый тест интеграции, и без кеша
// каждый вызов заводил бы свой пул на сотню незакрываемых соединений
var proxyTransports sync.Map

var localSuffixes = []string{".local", ".lan", ".internal", ".home", ".home.arpa"}

// возвращает интерфейсный nil, а не типизированный *http.Transport: присвоенный
// в http.Client.Transport типизированный nil паникует вместо отката на DefaultTransport
func ProxyTransport(raw string) http.RoundTripper {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
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
		// на битом адресе отказываем, а не откатываемся на прямое соединение: адрес
		// задан осознанно, и молчаливый выход с реального IP хуже отказа с ошибкой
		tr.Proxy = func(*http.Request) (*url.URL, error) {
			return nil, fmt.Errorf("invalid proxy url %q", raw)
		}
		return tr
	}
	// через httpproxy, а не через http.ProxyURL напрямую: тот отправляет в прокси
	// вообще всё, а здесь остаются исключения самого Go (localhost, loopback) и
	// NO_PROXY, которым пользователь выводит из-под прокси свои локальные адреса
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

// первая непустая, как это делает сам Go, а не склейка обеих: no_proxy=* из базового
// образа иначе перебивал бы NO_PROXY пользователя и глушил прокси целиком
func noProxyEnv() string {
	if v := os.Getenv("NO_PROXY"); v != "" {
		return v
	}
	return os.Getenv("no_proxy")
}

// httpproxy исключает только localhost и loopback, а рядом с приложением живут ещё
// вебхук на домашний сервер и соседи по docker-сети: гнать их через внешний прокси
// незачем, а отдавать его оператору имена раздач из тела вебхука тем более
func isLocalTarget(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	// имя без точки в публичном DNS не живёт: это сосед по docker-сети или /etc/hosts
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

// локальные сервисы (flaresolverr, торрент-клиент) стоят рядом с приложением и через
// прокси не ходят: из HTTP(S)_PROXY Go исключает только localhost, но не имена хостов
// в docker-сети, поэтому прямое соединение задаём явно, а не надеемся на пустое окружение
func DirectTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return tr
}
