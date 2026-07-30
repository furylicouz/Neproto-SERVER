package comparativelab

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

type MeasureConfig struct {
	RunID          string
	Implementation string
	Profile        string
	Transport      string
	Network        string
	Endpoint       string
	URL            string
	ProxyURL       string
	Runs           int
	ExpectedBytes  int64
	Timeout        time.Duration
	Warm           bool
	AddressFamily  string
	TargetAddress  string
	TLSConfig      *tls.Config
}

type requestTiming struct {
	mu        sync.Mutex
	connect   time.Duration
	firstByte time.Time
}

func Measure(ctx context.Context, config MeasureConfig) ([]Sample, error) {
	target, proxyAddress, err := validateMeasureConfig(config)
	if err != nil {
		return nil, err
	}

	samples := make([]Sample, 0, config.Runs)
	var warmTransport *http.Transport
	if config.Warm {
		warmTransport, err = newTransport(config, proxyAddress, nil)
		if err != nil {
			return nil, err
		}
		defer warmTransport.CloseIdleConnections()
	}

	for iteration := 1; iteration <= config.Runs; iteration++ {
		if err := ctx.Err(); err != nil {
			return samples, err
		}
		timing := &requestTiming{}
		transport := warmTransport
		if transport == nil {
			transport, err = newTransport(config, proxyAddress, timing)
			if err != nil {
				return nil, err
			}
		} else {
			transport.DialContext, err = measuredDialer(proxyAddress, timing, config.AddressFamily, config.TargetAddress)
			if err != nil {
				return nil, err
			}
		}

		sample := measureRequest(ctx, config, target, iteration, transport, timing)
		samples = append(samples, sample)
		if !config.Warm {
			transport.CloseIdleConnections()
		}
	}
	return samples, nil
}

func measureRequest(ctx context.Context, config MeasureConfig, target *url.URL, iteration int, transport *http.Transport, timing *requestTiming) Sample {
	started := time.Now().UTC()
	sample := Sample{
		Schema: SchemaV1, Timestamp: started, RunID: config.RunID,
		Implementation: config.Implementation, Profile: config.Profile,
		Transport: config.Transport, Network: config.Network, Endpoint: config.Endpoint,
		Iteration: iteration,
	}

	requestContext, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() {
		timing.mu.Lock()
		if timing.firstByte.IsZero() {
			timing.firstByte = time.Now()
		}
		timing.mu.Unlock()
	}}
	requestContext = httptrace.WithClientTrace(requestContext, trace)
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target.String(), nil)
	if err != nil {
		sample.ErrorCategory = "request"
		return sample
	}
	request.Header.Set("Cache-Control", "no-store")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "NP2-Comparative-Lab/1")

	client := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirect disabled")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		sample.ErrorCategory = requestErrorCategory(requestContext, err)
		fillTimings(&sample, timing, started, time.Now())
		return sample
	}
	defer response.Body.Close()
	sample.HTTPStatus = response.StatusCode
	bytesRead, readErr := io.CopyBuffer(io.Discard, response.Body, make([]byte, 32*1024))
	finished := time.Now()
	sample.Bytes = bytesRead
	fillTimings(&sample, timing, started, finished)
	if readErr != nil {
		sample.ErrorCategory = requestErrorCategory(requestContext, readErr)
		if sample.ErrorCategory == "request" {
			sample.ErrorCategory = "read"
		}
		return sample
	}
	if response.StatusCode != http.StatusOK {
		sample.ErrorCategory = "http_status"
		return sample
	}
	if config.ExpectedBytes > 0 && bytesRead != config.ExpectedBytes {
		sample.ErrorCategory = "size"
		return sample
	}
	sample.Success = true
	return sample
}

func fillTimings(sample *Sample, timing *requestTiming, started, finished time.Time) {
	timing.mu.Lock()
	connect := timing.connect
	firstByte := timing.firstByte
	timing.mu.Unlock()
	sample.ConnectMS = milliseconds(connect)
	if !firstByte.IsZero() {
		sample.TTFBMS = milliseconds(firstByte.Sub(started))
	}
	total := finished.Sub(started)
	sample.TotalMS = milliseconds(total)
	if total > 0 && sample.Bytes > 0 {
		sample.ThroughputBPS = float64(sample.Bytes*8) / total.Seconds()
	}
}

func newTransport(config MeasureConfig, proxyAddress *url.URL, timing *requestTiming) (*http.Transport, error) {
	dialContext, err := measuredDialer(proxyAddress, timing, config.AddressFamily, config.TargetAddress)
	if err != nil {
		return nil, err
	}
	tlsConfig := config.TLSConfig
	if tlsConfig != nil {
		tlsConfig = tlsConfig.Clone()
	}
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            dialContext,
		ForceAttemptHTTP2:      true,
		DisableCompression:     true,
		DisableKeepAlives:      !config.Warm,
		MaxIdleConns:           2,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        15 * time.Second,
		TLSHandshakeTimeout:    config.Timeout,
		ResponseHeaderTimeout:  config.Timeout,
		MaxResponseHeaderBytes: 64 * 1024,
		TLSClientConfig:        tlsConfig,
	}, nil
}

func measuredDialer(proxyAddress *url.URL, timing *requestTiming, addressFamily, targetAddress string) (func(context.Context, string, string) (net.Conn, error), error) {
	base := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	var dial func(context.Context, string, string) (net.Conn, error)
	if proxyAddress == nil {
		dial = base.DialContext
	} else {
		socksDialer, err := proxy.SOCKS5("tcp", proxyAddress.Host, nil, base)
		if err != nil {
			return nil, err
		}
		contextDialer, ok := socksDialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS dialer does not support cancellation")
		}
		dial = contextDialer.DialContext
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		resolvedAddress, err := resolveDialAddress(ctx, address, addressFamily, targetAddress)
		if err != nil {
			return nil, err
		}
		started := time.Now()
		connection, err := dial(ctx, network, resolvedAddress)
		if timing != nil {
			timing.mu.Lock()
			if timing.connect == 0 {
				timing.connect = time.Since(started)
			}
			timing.mu.Unlock()
		}
		return connection, err
	}, nil
}

func resolveDialAddress(ctx context.Context, address, addressFamily, targetAddress string) (string, error) {
	if (addressFamily == "" || addressFamily == "auto") && targetAddress == "" {
		return address, nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	if targetAddress != "" {
		return net.JoinHostPort(targetAddress, port), nil
	}
	network := "ip" + addressFamily
	if parsed := net.ParseIP(host); parsed != nil {
		if addressFamily == "4" && parsed.To4() == nil || addressFamily == "6" && parsed.To4() != nil {
			return "", errors.New("target address does not match requested address family")
		}
		return net.JoinHostPort(parsed.String(), port), nil
	}
	addresses, err := net.DefaultResolver.LookupIP(ctx, network, host)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", errors.New("target has no address in requested family")
	}
	return net.JoinHostPort(addresses[0].String(), port), nil
}

func validateMeasureConfig(config MeasureConfig) (*url.URL, *url.URL, error) {
	for _, label := range []string{config.RunID, config.Implementation, config.Profile, config.Transport, config.Network, config.Endpoint} {
		if !safeLabel(label) {
			return nil, nil, errors.New("invalid lab label")
		}
	}
	if config.Implementation != "direct" && config.Implementation != "np2" && config.Implementation != "vless" {
		return nil, nil, errors.New("unsupported implementation")
	}
	if config.Runs < 1 || config.Runs > 1_000 || config.ExpectedBytes < 0 || config.ExpectedBytes > 10<<30 {
		return nil, nil, errors.New("invalid measurement bounds")
	}
	if config.Timeout < 100*time.Millisecond || config.Timeout > 5*time.Minute {
		return nil, nil, errors.New("invalid measurement timeout")
	}
	if config.AddressFamily != "" && config.AddressFamily != "auto" && config.AddressFamily != "4" && config.AddressFamily != "6" {
		return nil, nil, errors.New("address family must be auto, 4, or 6")
	}
	if config.TargetAddress != "" {
		parsed := net.ParseIP(config.TargetAddress)
		if parsed == nil || config.AddressFamily == "4" && parsed.To4() == nil || config.AddressFamily == "6" && parsed.To4() != nil {
			return nil, nil, errors.New("target address pin must be an IP in the selected family")
		}
		config.TargetAddress = parsed.String()
	}
	target, err := url.Parse(config.URL)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		return nil, nil, errors.New("measurement target must be an HTTPS URL without user info or fragment")
	}
	if config.ProxyURL == "" {
		return target, nil, nil
	}
	proxyAddress, err := url.Parse(config.ProxyURL)
	if err != nil || proxyAddress.User != nil || proxyAddress.Path != "" || proxyAddress.RawQuery != "" || proxyAddress.Fragment != "" {
		return nil, nil, errors.New("invalid SOCKS proxy URL")
	}
	if proxyAddress.Scheme != "socks5" && proxyAddress.Scheme != "socks5h" {
		return nil, nil, errors.New("proxy must use socks5 or socks5h")
	}
	host := strings.Trim(strings.ToLower(proxyAddress.Hostname()), "[]")
	if host != "localhost" {
		address := net.ParseIP(host)
		if address == nil || !address.IsLoopback() {
			return nil, nil, errors.New("SOCKS proxy must listen on loopback")
		}
	}
	if proxyAddress.Port() == "" {
		return nil, nil, errors.New("SOCKS proxy port is required")
	}
	return target, proxyAddress, nil
}

func requestErrorCategory(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns"
	}
	var recordError tls.RecordHeaderError
	if errors.As(err, &recordError) {
		return "tls"
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		if operationError.Op == "dial" {
			return "connect"
		}
		return "network"
	}
	if strings.Contains(strings.ToLower(err.Error()), "redirect") {
		return "redirect"
	}
	return "request"
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
