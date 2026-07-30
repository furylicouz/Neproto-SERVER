package selfupdate

import (
	"errors"
	"strconv"
	"strings"

	"neproto.local/chameleon/internal/admin"
)

func InstallerArguments(installation admin.Installation, acmeEmail string) ([]string, error) {
	if installation.Mode != admin.ModeBareMetal && installation.Mode != admin.ModeDocker {
		return nil, errors.New("unsupported installed deployment mode")
	}
	if installation.Domain == "" || len(installation.ServerAddresses) == 0 || installation.WebPort < 1024 {
		return nil, errors.New("incomplete installed topology")
	}
	for _, value := range append(append([]string{}, installation.ServerAddresses...), installation.Domain, installation.WebDomain, installation.HTTPSPath, installation.WebRTCPath, installation.HTTP3Path, acmeEmail) {
		if strings.ContainsAny(value, "\r\n\x00") {
			return nil, errors.New("installed topology contains control characters")
		}
	}
	arguments := []string{
		"--mode", installation.Mode,
		"--domain", installation.Domain,
		"--addresses", strings.Join(installation.ServerAddresses, ","),
	}
	if acmeEmail != "" {
		arguments = append(arguments, "--email", acmeEmail)
	}
	if installation.WebDomain != "" {
		arguments = append(arguments, "--web-domain", installation.WebDomain)
	}
	arguments = append(arguments,
		"--web-port", strconv.Itoa(installation.WebPort),
		"--https-path", installation.HTTPSPath,
		"--webrtc-path", installation.WebRTCPath,
		"--http3-path", installation.HTTP3Path,
		"--non-interactive",
	)
	return arguments, nil
}
