package multiuser

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"
)

func NewDeviceTransport(meshProxy string) (*http.Transport, error) {
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 15 * time.Second, MaxIdleConnsPerHost: 4}
	if meshProxy == "" {
		return transport, nil
	}
	endpoint, err := url.Parse(meshProxy)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Port() == "" {
		return nil, errors.New("FLEET_MESH_PROXY 必须是 HTTP loopback 代理 origin")
	}
	address := net.ParseIP(endpoint.Hostname())
	if address == nil || !address.IsLoopback() {
		return nil, errors.New("FLEET_MESH_PROXY 只能连接 loopback")
	}
	transport.Proxy = http.ProxyURL(endpoint)
	return transport, nil
}
