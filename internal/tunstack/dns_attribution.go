package tunstack

import (
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	dnsAttributionMaxPacket    = 65535
	dnsAttributionMaxPending   = 4096
	dnsAttributionMaxAddresses = 8192
	dnsAttributionPendingTTL   = 30 * time.Second
	dnsAttributionMaximumTTL   = time.Hour
)

type dnsAttribution struct {
	mu        sync.Mutex
	now       func() time.Time
	pending   map[uint16][]dnsPending
	addresses map[netip.Addr]dnsAddress
	count     int
	queries   uint64
	responses uint64
	hits      uint64
	misses    uint64
}

// DNSAttributionStats contains destination-free counters suitable for the
// PacketTunnel diagnostic surface. Cached is a bounded current gauge; the
// remaining fields are monotonic for the lifetime of the tunnel.
type DNSAttributionStats struct {
	Queries               uint64
	Responses             uint64
	Hits                  uint64
	Misses                uint64
	Cached                uint64
	FirstFlightDomainHits uint64
	FirstFlightFallbacks  uint64
}

type dnsPending struct {
	questions []dnsQuestion
	expires   time.Time
}

type dnsQuestion struct {
	name  string
	type_ dnsmessage.Type
	class dnsmessage.Class
}

type dnsAddress struct {
	domain  string
	expires time.Time
}

func newDNSAttribution(now func() time.Time) *dnsAttribution {
	if now == nil {
		now = time.Now
	}
	return &dnsAttribution{
		now: now, pending: make(map[uint16][]dnsPending), addresses: make(map[netip.Addr]dnsAddress),
	}
}

func (cache *dnsAttribution) observeQuery(payload []byte) {
	id, questions, ok := validatedDNSQuery(payload)
	if !ok {
		return
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	cache.purgeExpiredLocked(now)
	if cache.count >= dnsAttributionMaxPending {
		cache.evictPendingLocked()
	}
	cache.pending[id] = append(cache.pending[id], dnsPending{
		questions: questions, expires: now.Add(dnsAttributionPendingTTL),
	})
	cache.count++
	cache.queries++
}

func (cache *dnsAttribution) discardQuery(payload []byte) {
	if cache == nil {
		return
	}
	id, questions, ok := validatedDNSQuery(payload)
	if !ok {
		return
	}
	cache.mu.Lock()
	cache.takePendingLocked(id, questions)
	cache.mu.Unlock()
}

func validatedDNSQuery(payload []byte) (uint16, []dnsQuestion, bool) {
	message, ok := unpackDNSMessage(payload)
	if !ok || message.Header.Response {
		return 0, nil, false
	}
	questions, ok := dnsQuestions(message.Questions)
	if !ok {
		return 0, nil, false
	}
	for _, question := range questions {
		if question.class != dnsmessage.ClassINET ||
			(question.type_ != dnsmessage.TypeA && question.type_ != dnsmessage.TypeAAAA) {
			return 0, nil, false
		}
	}
	return message.Header.ID, questions, true
}

func (cache *dnsAttribution) observeResponse(payload []byte) {
	message, ok := unpackDNSMessage(payload)
	if !ok || !message.Header.Response || message.Header.Truncated ||
		message.Header.RCode != dnsmessage.RCodeSuccess || len(message.Answers) > 256 {
		return
	}
	questions, ok := dnsQuestions(message.Questions)
	if !ok {
		return
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	cache.purgeExpiredLocked(now)
	pending, ok := cache.takePendingLocked(message.Header.ID, questions)
	if !ok {
		return
	}
	cache.responses++

	// Track the validated CNAME chain from each original question. This keeps
	// unrelated answer records from poisoning the short-lived attribution map.
	origins := make(map[string]string, len(pending.questions))
	chainTTL := make(map[string]uint32, len(pending.questions))
	for _, question := range pending.questions {
		origins[question.name] = question.name
		chainTTL[question.name] = uint32(dnsAttributionMaximumTTL / time.Second)
	}
	for pass := 0; pass < len(message.Answers); pass++ {
		changed := false
		for _, answer := range message.Answers {
			cname, isCNAME := answer.Body.(*dnsmessage.CNAMEResource)
			if !isCNAME {
				continue
			}
			owner, ownerOK := canonicalDNSName(answer.Header.Name.String())
			target, targetOK := canonicalDNSName(cname.CNAME.String())
			origin, allowed := origins[owner]
			if !ownerOK || !targetOK || !allowed {
				continue
			}
			if _, exists := origins[target]; exists {
				continue
			}
			origins[target] = origin
			chainTTL[target] = minimumDNSSeconds(chainTTL[owner], answer.Header.TTL)
			changed = true
		}
		if !changed {
			break
		}
	}

	for _, answer := range message.Answers {
		owner, valid := canonicalDNSName(answer.Header.Name.String())
		origin, allowed := origins[owner]
		if !valid || !allowed {
			continue
		}
		ttl := minimumDNSSeconds(chainTTL[owner], answer.Header.TTL)
		var address netip.Addr
		switch resource := answer.Body.(type) {
		case *dnsmessage.AResource:
			address = netip.AddrFrom4(resource.A).Unmap()
		case *dnsmessage.AAAAResource:
			address = netip.AddrFrom16(resource.AAAA).Unmap()
		default:
			continue
		}
		cache.putAddressLocked(address, origin, ttl, now)
	}
}

func (cache *dnsAttribution) domainFor(address netip.Addr) (string, bool) {
	if cache == nil || !address.IsValid() {
		return "", false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	address = address.Unmap()
	entry, ok := cache.addresses[address]
	if !ok {
		cache.misses++
		return "", false
	}
	if !cache.now().Before(entry.expires) {
		delete(cache.addresses, address)
		cache.misses++
		return "", false
	}
	cache.hits++
	return entry.domain, true
}

func (cache *dnsAttribution) stats() DNSAttributionStats {
	if cache == nil {
		return DNSAttributionStats{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.purgeExpiredLocked(cache.now())
	return DNSAttributionStats{
		Queries: cache.queries, Responses: cache.responses,
		Hits: cache.hits, Misses: cache.misses, Cached: uint64(len(cache.addresses)),
	}
}

func unpackDNSMessage(payload []byte) (dnsmessage.Message, bool) {
	if len(payload) < 12 || len(payload) > dnsAttributionMaxPacket {
		return dnsmessage.Message{}, false
	}
	var message dnsmessage.Message
	if err := message.Unpack(payload); err != nil {
		return dnsmessage.Message{}, false
	}
	return message, true
}

func dnsQuestions(questions []dnsmessage.Question) ([]dnsQuestion, bool) {
	if len(questions) == 0 || len(questions) > 8 {
		return nil, false
	}
	result := make([]dnsQuestion, 0, len(questions))
	for _, question := range questions {
		name, ok := canonicalDNSName(question.Name.String())
		if !ok {
			return nil, false
		}
		result = append(result, dnsQuestion{name: name, type_: question.Type, class: question.Class})
	}
	return result, true
}

func canonicalDNSName(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || len(value) > 253 {
		return "", false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '_' {
				return "", false
			}
		}
	}
	return value, true
}

func (cache *dnsAttribution) takePendingLocked(id uint16, questions []dnsQuestion) (dnsPending, bool) {
	entries := cache.pending[id]
	for index, entry := range entries {
		if !sameDNSQuestions(entry.questions, questions) {
			continue
		}
		entries = append(entries[:index], entries[index+1:]...)
		if len(entries) == 0 {
			delete(cache.pending, id)
		} else {
			cache.pending[id] = entries
		}
		cache.count--
		return entry, true
	}
	return dnsPending{}, false
}

func sameDNSQuestions(left, right []dnsQuestion) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (cache *dnsAttribution) putAddressLocked(address netip.Addr, domain string, ttl uint32, now time.Time) {
	if ttl == 0 || !address.IsValid() {
		return
	}
	if len(cache.addresses) >= dnsAttributionMaxAddresses {
		cache.evictAddressLocked()
	}
	duration := time.Duration(ttl) * time.Second
	if duration > dnsAttributionMaximumTTL {
		duration = dnsAttributionMaximumTTL
	}
	cache.addresses[address.Unmap()] = dnsAddress{domain: domain, expires: now.Add(duration)}
}

func minimumDNSSeconds(left, right uint32) uint32 {
	if left == 0 || right == 0 {
		return 0
	}
	if left < right {
		return left
	}
	return right
}

func (cache *dnsAttribution) purgeExpiredLocked(now time.Time) {
	for id, entries := range cache.pending {
		kept := entries[:0]
		for _, entry := range entries {
			if now.Before(entry.expires) {
				kept = append(kept, entry)
			} else {
				cache.count--
			}
		}
		if len(kept) == 0 {
			delete(cache.pending, id)
		} else {
			cache.pending[id] = kept
		}
	}
	for address, entry := range cache.addresses {
		if !now.Before(entry.expires) {
			delete(cache.addresses, address)
		}
	}
}

func (cache *dnsAttribution) evictPendingLocked() {
	var selectedID uint16
	selectedIndex := -1
	var selectedExpiry time.Time
	for id, entries := range cache.pending {
		for index, entry := range entries {
			if selectedIndex < 0 || entry.expires.Before(selectedExpiry) {
				selectedID, selectedIndex, selectedExpiry = id, index, entry.expires
			}
		}
	}
	if selectedIndex < 0 {
		return
	}
	entries := cache.pending[selectedID]
	entries = append(entries[:selectedIndex], entries[selectedIndex+1:]...)
	if len(entries) == 0 {
		delete(cache.pending, selectedID)
	} else {
		cache.pending[selectedID] = entries
	}
	cache.count--
}

func (cache *dnsAttribution) evictAddressLocked() {
	var selected netip.Addr
	var selectedExpiry time.Time
	for address, entry := range cache.addresses {
		if !selected.IsValid() || entry.expires.Before(selectedExpiry) {
			selected, selectedExpiry = address, entry.expires
		}
	}
	if selected.IsValid() {
		delete(cache.addresses, selected)
	}
}
