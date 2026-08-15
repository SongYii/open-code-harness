package openaicompat

import (
	"net"
	"net/http"
	"strings"
	"time"
)

func cloneHTTPClient(src *http.Client, headerTimeout time.Duration) *http.Client {
	if src == nil {
		return &http.Client{
			Transport:     newPrivateTransport(headerTimeout),
			CheckRedirect: useLastResponse,
		}
	}
	cloned := *src
	switch transport := src.Transport.(type) {
	case *http.Transport:
		if transport != nil {
			clonedTransport := transport.Clone()
			clonedTransport.ResponseHeaderTimeout = headerTimeout
			cloned.Transport = clonedTransport
		} else {
			cloned.Transport = cloneDefaultTransport(headerTimeout)
		}
	case nil:
		cloned.Transport = cloneDefaultTransport(headerTimeout)
	}
	cloned.CheckRedirect = useLastResponse
	return &cloned
}

func newPrivateTransport(headerTimeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func cloneDefaultTransport(headerTimeout time.Duration) http.RoundTripper {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport != nil {
		cloned := transport.Clone()
		cloned.ResponseHeaderTimeout = headerTimeout
		return cloned
	}
	return newPrivateTransport(headerTimeout)
}

func useLastResponse(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
