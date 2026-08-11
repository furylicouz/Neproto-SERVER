package windowsclient

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"neproto.local/chameleon/internal/cluster"
)

var ErrControllerBusy = errors.New("NeProto tunnel operation already in progress")

type BackendStatus struct {
	Carrier                  string   `json:"carrier,omitempty"`
	ServerAddresses          []string `json:"server_addresses,omitempty"`
	UploadBytesPerSecond     int64    `json:"upload_bytes_per_second"`
	DownloadBytesPerSecond   int64    `json:"download_bytes_per_second"`
	UploadTotalBytes         int64    `json:"upload_total_bytes"`
	DownloadTotalBytes       int64    `json:"download_total_bytes"`
	UDPMode                  string   `json:"udp_mode,omitempty"`
	CarrierPoolTarget        int64    `json:"carrier_pool_target"`
	CarrierPoolHealthy       int64    `json:"carrier_pool_healthy"`
	CarrierPoolAssignments   int64    `json:"carrier_pool_assignments"`
	NP2ConnectMilliseconds   int64    `json:"np2_connect_milliseconds,omitempty"`
	WindowsSetupMilliseconds int64    `json:"windows_setup_milliseconds,omitempty"`
}

type Backend interface {
	SetRoutes([]byte) error
	Connect(context.Context, []byte, string) (BackendStatus, error)
	Disconnect(context.Context) error
	Snapshot() BackendStatus
	FetchCatalog(context.Context) ([]byte, error)
}

type Status struct {
	State             State     `json:"state"`
	LastError         string    `json:"last_error,omitempty"`
	SelectedProfileID string    `json:"selected_profile_id,omitempty"`
	ProfileName       string    `json:"profile_name,omitempty"`
	ServerIdentity    string    `json:"server_identity,omitempty"`
	ConnectedSince    time.Time `json:"connected_since,omitempty"`
	BackendStatus
}

type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Controller struct {
	mu             sync.Mutex
	store          *Store
	backend        Backend
	state          State
	lastError      string
	connectedSince time.Time
	cancel         context.CancelFunc
	generation     uint64
	logs           []LogEntry
}

func NewController(store *Store, backend Backend) *Controller {
	return &Controller{store: store, backend: backend, state: StateStopped, logs: make([]LogEntry, 0, 256)}
}

func (c *Controller) Connect() error {
	if c == nil || c.store == nil || c.backend == nil {
		return ErrControllerBusy
	}
	profile, raw, secret, err := c.store.Selected()
	if err != nil {
		return err
	}
	routes, err := json.Marshal(c.store.EffectiveRoutes(profile.ID))
	if err != nil {
		return err
	}
	if err := c.backend.SetRoutes(routes); err != nil {
		return err
	}
	c.mu.Lock()
	if c.state == StateConnecting || c.state == StateConnected || c.state == StateDisconnecting {
		c.mu.Unlock()
		return ErrControllerBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.generation++
	generation := c.generation
	c.cancel = cancel
	c.state = StateConnecting
	c.lastError = ""
	c.connectedSince = time.Time{}
	c.appendLogLocked("info", "Connecting to "+profile.ServerIdentity)
	c.mu.Unlock()

	go func() {
		status, connectErr := c.backend.Connect(ctx, raw, secret)
		c.mu.Lock()
		if c.generation != generation || c.state != StateConnecting {
			c.mu.Unlock()
			if connectErr == nil {
				_ = c.backend.Disconnect(context.Background())
			}
			return
		}
		c.cancel = nil
		if connectErr != nil {
			c.state = StateFailed
			c.lastError = safeError(connectErr)
			c.appendLogLocked("error", c.lastError)
			c.mu.Unlock()
			return
		}
		c.state = StateConnected
		c.connectedSince = time.Now().UTC()
		c.appendLogLocked("info", "Connected using "+status.Carrier)
		c.appendLogLocked("info", fmt.Sprintf("Connection timing: NP/2 %d ms, Windows %d ms",
			status.NP2ConnectMilliseconds, status.WindowsSetupMilliseconds))
		c.mu.Unlock()
	}()
	return nil
}

func (c *Controller) SyncCatalog() (result ClientCatalogState, err error) {
	defer func() {
		if err == nil {
			return
		}
		c.mu.Lock()
		c.appendLogLocked("warning", "Cluster catalogue synchronization failed: "+safeError(err))
		c.mu.Unlock()
	}()
	status := c.Status()
	if status.State != StateConnected || status.SelectedProfileID == "" {
		return ClientCatalogState{}, ErrControllerBusy
	}
	var selected Profile
	for _, profile := range c.store.Profiles() {
		if profile.ID == status.SelectedProfileID {
			selected = profile
			break
		}
	}
	if selected.ClusterID == "" || selected.CatalogPublicKey == "" {
		return ClientCatalogState{}, cluster.ErrInvalidCatalog
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(selected.CatalogPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return ClientCatalogState{}, cluster.ErrInvalidCatalogSignature
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	raw, err := c.backend.FetchCatalog(ctx)
	cancel()
	if err != nil {
		return ClientCatalogState{}, err
	}
	catalog, err := cluster.DecodeAndVerifyCatalogEnvelope(raw, ed25519.PublicKey(publicKey), selected.ClusterID,
		c.store.CatalogRevision(selected.ID), time.Now().UTC())
	if err != nil {
		return ClientCatalogState{}, err
	}
	if err := c.store.ApplyCatalog(selected.ID, catalog); err != nil {
		return ClientCatalogState{}, err
	}
	state, ok := c.store.Catalog(selected.ID)
	if !ok {
		return ClientCatalogState{}, cluster.ErrInvalidCatalog
	}
	c.mu.Lock()
	c.appendLogLocked("info", "Cluster catalogue synchronized")
	c.mu.Unlock()
	return state, nil
}

type RouteView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Mandatory   bool   `json:"mandatory"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source"`
}

func (c *Controller) Routes() []RouteView {
	routes := c.store.EffectiveRoutes(c.store.SelectedProfileID())
	result := make([]RouteView, 0, len(routes))
	for _, route := range routes {
		result = append(result, RouteView{ID: route.ID, Name: route.Name, Description: describeRoute(route),
			Mandatory: route.Mandatory, Enabled: route.Enabled, Source: string(route.Source)})
	}
	return result
}

func (c *Controller) UpsertLocalRoute(route cluster.Route) error {
	return c.store.UpsertLocalRoute(c.store.SelectedProfileID(), route)
}

func (c *Controller) RemoveLocalRoute(id string) error {
	return c.store.RemoveLocalRoute(c.store.SelectedProfileID(), id)
}

func describeRoute(route cluster.Route) string {
	selectors := append([]string(nil), route.Match.DomainSuffixes...)
	selectors = append(selectors, route.Match.CIDRs...)
	for _, value := range route.Match.GeoSiteCategories {
		selectors = append(selectors, "GeoSite: "+value)
	}
	for _, value := range route.Match.GeoIPCountries {
		selectors = append(selectors, "GeoIP: "+strings.ToUpper(value))
	}
	if len(selectors) == 0 {
		selectors = append(selectors, "весь трафик")
	}
	action := string(route.Action.Kind)
	if len(route.Action.NodeIDs) > 0 {
		action += " " + strings.Join(route.Action.NodeIDs, " → ")
	}
	return strings.Join(selectors, ", ") + " • " + action
}

func (c *Controller) Profiles() []Profile { return c.store.Profiles() }

func (c *Controller) ImportProfile(uri string) (Profile, error) {
	if !c.profileMutationAllowed() {
		return Profile{}, ErrControllerBusy
	}
	profile, err := c.store.Import(uri)
	if err == nil {
		c.mu.Lock()
		c.appendLogLocked("info", "Imported profile "+profile.Name)
		c.mu.Unlock()
	}
	return profile, err
}

func (c *Controller) SelectProfile(id string) error {
	if !c.profileMutationAllowed() {
		return ErrControllerBusy
	}
	return c.store.Select(id)
}

func (c *Controller) RemoveProfile(id string) error {
	if !c.profileMutationAllowed() {
		return ErrControllerBusy
	}
	return c.store.Remove(id)
}

func (c *Controller) profileMutationAllowed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == StateStopped || c.state == StateFailed
}

func (c *Controller) Disconnect() error {
	if c == nil || c.backend == nil {
		return nil
	}
	c.mu.Lock()
	if c.state == StateStopped {
		c.lastError = ""
		c.mu.Unlock()
		return nil
	}
	if c.state == StateDisconnecting {
		c.mu.Unlock()
		return ErrControllerBusy
	}
	c.generation++
	c.state = StateDisconnecting
	cancel := c.cancel
	c.cancel = nil
	c.appendLogLocked("info", "Disconnect requested")
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	err := c.backend.Disconnect(ctx)
	stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectedSince = time.Time{}
	if err != nil {
		c.state = StateFailed
		c.lastError = safeError(err)
		c.appendLogLocked("error", c.lastError)
		return err
	}
	c.state = StateStopped
	c.lastError = ""
	c.appendLogLocked("info", "Disconnected")
	return nil
}

// Shutdown performs the fast user-visible disconnect and then waits for any
// durable Windows route cleanup before the service process exits.
func (c *Controller) Shutdown(ctx context.Context) error {
	disconnectErr := c.Disconnect()
	var cleanupErr error
	if waiter, ok := c.backend.(interface{ WaitForCleanup(context.Context) error }); ok {
		cleanupErr = waiter.WaitForCleanup(ctx)
	}
	return errors.Join(disconnectErr, cleanupErr)
}

func (c *Controller) Status() Status {
	if c == nil {
		return Status{State: StateFailed, LastError: "controller unavailable"}
	}
	c.mu.Lock()
	status := Status{State: c.state, LastError: c.lastError, ConnectedSince: c.connectedSince}
	c.mu.Unlock()
	status.SelectedProfileID = c.store.SelectedProfileID()
	for _, profile := range c.store.Profiles() {
		if profile.ID == status.SelectedProfileID {
			status.ProfileName, status.ServerIdentity = profile.Name, profile.ServerIdentity
			break
		}
	}
	if status.State == StateConnected {
		status.BackendStatus = c.backend.Snapshot()
	}
	return status
}

func (c *Controller) Logs(limit int) []LogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	start := len(c.logs) - limit
	if start < 0 {
		start = 0
	}
	return append([]LogEntry(nil), c.logs[start:]...)
}

func (c *Controller) appendLogLocked(level, message string) {
	message = strings.TrimSpace(message)
	if len(message) > 512 {
		message = message[:512]
	}
	c.logs = append(c.logs, LogEntry{Time: time.Now().UTC(), Level: level, Message: message})
	if len(c.logs) > 256 {
		c.logs = append(c.logs[:0], c.logs[len(c.logs)-256:]...)
	}
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if index := strings.Index(message, "np2://"); index >= 0 {
		message = message[:index] + "<redacted>"
	}
	if len(message) > 512 {
		message = message[:512]
	}
	if message == "" {
		return "operation failed"
	}
	return message
}
