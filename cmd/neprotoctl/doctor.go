package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/carrier/http3wt"
)

func runDoctor(
	manager *admin.Manager,
	controller serviceController,
	stdout, stderr io.Writer,
) int {
	failed := false
	check := func(name string, err error) {
		if err == nil {
			fmt.Fprintf(stdout, "[OK] %s\n", name)
			return
		}
		failed = true
		fmt.Fprintf(stdout, "[FAIL] %s: %v\n", name, err)
	}

	installation := manager.Installation()
	users, err := manager.ListUsers()
	check("installation state", err)
	if err == nil {
		active, revoked := 0, 0
		for _, user := range users {
			if user.Status == admin.StatusActive {
				active++
			} else {
				revoked++
			}
		}
		fmt.Fprintf(stdout, "     users: %d active, %d revoked\n", active, revoked)
	}
	check("NP/2 configuration", controller.Validate(io.Discard, stderr))
	snapshot := controller.Snapshot()
	check("NP/2 service", activeStateError(snapshot.NP2))
	if installation.WebEnabled {
		check("Web admin service", activeStateError(snapshot.Web))
	}
	check("Caddy ingress", activeStateError(snapshot.Ingress))
	check("public HTTPS + HTTP/3", controller.PublicProbe(installation))

	if failed {
		fmt.Fprintln(stdout, "Doctor found one or more problems.")
		return 1
	}
	fmt.Fprintln(stdout, "All production checks passed.")
	return 0
}

func activeStateError(state string) error {
	if state == "active" || state == "running" || state == "test" {
		return nil
	}
	return fmt.Errorf("state is %s", displayState(state))
}

func (controller commandController) PublicProbe(installation admin.Installation) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, installation.Domain)
	if err != nil {
		return fmt.Errorf("resolve domain: %w", err)
	}
	configured := make(map[netip.Addr]struct{}, len(installation.ServerAddresses))
	configuredAddresses := make([]netip.Addr, 0, len(installation.ServerAddresses))
	for _, value := range installation.ServerAddresses {
		address, parseErr := netip.ParseAddr(value)
		if parseErr != nil {
			return fmt.Errorf("invalid configured address: %w", parseErr)
		}
		address = address.Unmap()
		configured[address] = struct{}{}
		configuredAddresses = append(configuredAddresses, address)
	}
	matched := false
	for _, value := range resolved {
		address, ok := netip.AddrFromSlice(value.IP)
		if !ok {
			continue
		}
		if _, exists := configured[address.Unmap()]; exists {
			matched = true
			break
		}
	}
	if !matched {
		return errors.New("DNS does not contain a configured server address")
	}

	client := &http.Client{
		Timeout:       8 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+installation.Domain+"/", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("unexpected HTTPS status %s", strings.TrimSpace(response.Status))
	}
	if installation.HTTP3Path != "" {
		http3Context, cancelHTTP3 := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancelHTTP3()
		connection, dialErr := http3wt.Dial(http3Context, http3wt.DialConfig{
			URL:             "https://" + installation.Domain + installation.HTTP3Path,
			ServerAddresses: configuredAddresses, HandshakeIdleTimeout: 5 * time.Second,
		})
		if dialErr != nil {
			return fmt.Errorf("public HTTP/3 WebTransport: %w", dialErr)
		}
		_ = connection.Close()
	}
	return nil
}
