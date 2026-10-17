package relay

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyHTTPSConnectPreservesTLSAndHidesProxyCredentialsFromUpstream(t *testing.T) {
	previousAllowPrivateDial := allowPrivateDial.Load()
	SetAllowPrivateDial(true)
	t.Cleanup(func() { SetAllowPrivateDial(previousAllowPrivateDial); SetOutboundProxyURL("") })
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials leaked to upstream")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: ready\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()
	var tunnels atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.Write([]byte("warm"))
			return
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:secret")) {
			t.Error("CONNECT did not authenticate")
		}
		target, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, "dial", 502)
			return
		}
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			target.Close()
			return
		}
		tunnels.Add(1)
		defer client.Close()
		defer target.Close()
		buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffered.Flush()
		go func() { io.Copy(target, buffered); target.Close() }()
		io.Copy(client, target)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("user", "secret")
	SetOutboundProxyURL(proxyURL.String())
	transport := NewSecureTransport().(*outboundTransport)
	client := &http.Client{Transport: transport, Timeout: time.Second * 3}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://8.8.8.8/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	transport.transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	response, err = client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "data: ready\n\n" || tunnels.Load() != 1 {
		t.Fatalf("CONNECT stream failed: %q %v", body, err)
	}
}
