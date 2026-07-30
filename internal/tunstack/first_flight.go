package tunstack

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
)

const firstFlightMaximumBytes = 16 << 10

type firstFlightDecision uint8

const (
	firstFlightNeedMore firstFlightDecision = iota
	firstFlightUseNumeric
	firstFlightUseDomain
)

func inspectFirstFlight(payload []byte) (string, firstFlightDecision) {
	if len(payload) == 0 {
		return "", firstFlightNeedMore
	}
	if payload[0] == 0x16 {
		return inspectTLSClientHello(payload)
	}
	return inspectHTTPHost(payload)
}

func inspectTLSClientHello(payload []byte) (string, firstFlightDecision) {
	if len(payload) < 5 {
		return "", firstFlightNeedMore
	}
	if payload[0] != 0x16 || payload[1] != 3 {
		return "", firstFlightUseNumeric
	}
	recordLength := int(binary.BigEndian.Uint16(payload[3:5]))
	if recordLength < 4 || recordLength > firstFlightMaximumBytes-5 {
		return "", firstFlightUseNumeric
	}
	if len(payload) < 5+recordLength {
		return "", firstFlightNeedMore
	}
	record := payload[5 : 5+recordLength]
	if len(record) < 4 || record[0] != 1 {
		return "", firstFlightUseNumeric
	}
	handshakeLength := int(record[1])<<16 | int(record[2])<<8 | int(record[3])
	if handshakeLength < 38 || handshakeLength > len(record)-4 {
		return "", firstFlightUseNumeric
	}
	body := record[4 : 4+handshakeLength]
	offset := 2 + 32
	if offset >= len(body) {
		return "", firstFlightUseNumeric
	}
	sessionLength := int(body[offset])
	offset++
	if !advanceWithin(&offset, sessionLength, len(body)) || offset+2 > len(body) {
		return "", firstFlightUseNumeric
	}
	cipherLength := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if cipherLength < 2 || cipherLength%2 != 0 || !advanceWithin(&offset, cipherLength, len(body)) || offset >= len(body) {
		return "", firstFlightUseNumeric
	}
	compressionLength := int(body[offset])
	offset++
	if compressionLength < 1 || !advanceWithin(&offset, compressionLength, len(body)) {
		return "", firstFlightUseNumeric
	}
	if offset == len(body) {
		return "", firstFlightUseNumeric
	}
	if offset+2 > len(body) {
		return "", firstFlightUseNumeric
	}
	extensionsLength := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if extensionsLength != len(body)-offset {
		return "", firstFlightUseNumeric
	}
	end := offset + extensionsLength
	for offset < end {
		if offset+4 > end {
			return "", firstFlightUseNumeric
		}
		extensionType := binary.BigEndian.Uint16(body[offset : offset+2])
		extensionLength := int(binary.BigEndian.Uint16(body[offset+2 : offset+4]))
		offset += 4
		if extensionLength > end-offset {
			return "", firstFlightUseNumeric
		}
		if extensionType == 0 {
			return inspectServerNameExtension(body[offset : offset+extensionLength])
		}
		offset += extensionLength
	}
	return "", firstFlightUseNumeric
}

func inspectServerNameExtension(extension []byte) (string, firstFlightDecision) {
	if len(extension) < 5 || int(binary.BigEndian.Uint16(extension[:2])) != len(extension)-2 {
		return "", firstFlightUseNumeric
	}
	for offset := 2; offset < len(extension); {
		if offset+3 > len(extension) {
			return "", firstFlightUseNumeric
		}
		nameType := extension[offset]
		nameLength := int(binary.BigEndian.Uint16(extension[offset+1 : offset+3]))
		offset += 3
		if nameLength == 0 || nameLength > len(extension)-offset {
			return "", firstFlightUseNumeric
		}
		if nameType == 0 {
			name, ok := canonicalDNSName(string(extension[offset : offset+nameLength]))
			if !ok || net.ParseIP(name) != nil {
				return "", firstFlightUseNumeric
			}
			return name, firstFlightUseDomain
		}
		offset += nameLength
	}
	return "", firstFlightUseNumeric
}

func inspectHTTPHost(payload []byte) (string, firstFlightDecision) {
	if len(payload) > firstFlightMaximumBytes {
		return "", firstFlightUseNumeric
	}
	lineEnd := bytes.Index(payload, []byte("\r\n"))
	if lineEnd < 0 {
		if couldBeHTTPPrefix(payload) {
			return "", firstFlightNeedMore
		}
		return "", firstFlightUseNumeric
	}
	requestLine := string(payload[:lineEnd])
	parts := strings.Fields(requestLine)
	if len(parts) != 3 || !isHTTPMethod(parts[0]) || !strings.HasPrefix(parts[2], "HTTP/1.") {
		return "", firstFlightUseNumeric
	}
	headersStart := lineEnd + 2
	relativeHeadersEnd := bytes.Index(payload[headersStart:], []byte("\r\n\r\n"))
	if relativeHeadersEnd < 0 {
		return "", firstFlightNeedMore
	}
	headersEnd := headersStart + relativeHeadersEnd
	for _, line := range strings.Split(string(payload[headersStart:headersEnd]), "\r\n") {
		name, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "host") {
			continue
		}
		host := strings.TrimSpace(value)
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			host = parsedHost
		} else {
			host = strings.TrimSuffix(host, ":")
		}
		host, ok := canonicalDNSName(host)
		if !ok || net.ParseIP(host) != nil {
			return "", firstFlightUseNumeric
		}
		return host, firstFlightUseDomain
	}
	return "", firstFlightUseNumeric
}

func couldBeHTTPPrefix(payload []byte) bool {
	upper := strings.ToUpper(string(payload))
	for _, method := range []string{"GET ", "HEAD ", "POST ", "PUT ", "DELETE ", "OPTIONS ", "PATCH ", "CONNECT "} {
		if strings.HasPrefix(method, upper) || strings.HasPrefix(upper, method) {
			return true
		}
	}
	return false
}

func isHTTPMethod(value string) bool {
	switch value {
	case "GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "PATCH", "CONNECT":
		return true
	default:
		return false
	}
}

func advanceWithin(offset *int, count, limit int) bool {
	if count < 0 || *offset > limit-count {
		return false
	}
	*offset += count
	return true
}
