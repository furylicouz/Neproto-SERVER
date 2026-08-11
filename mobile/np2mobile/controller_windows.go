//go:build windows

package np2mobile

import "github.com/xjasonlyu/tun2socks/v2/core/device"

// StartWindowsPacketTunnel attaches a configured Wintun device to the active
// authenticated NP/2 session. It is excluded from gomobile Apple builds.
func StartWindowsPacketTunnel(endpoint device.Device, mtu int64) error {
	if mtu < 0 || mtu > int64(^uint32(0)) {
		return ErrInvalidTunnelFD
	}
	return defaultController.startTunnelDevice(endpoint, uint32(mtu))
}
