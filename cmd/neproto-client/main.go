package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
)

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 1 && arguments[0] == "version" {
		fmt.Fprintf(stdout, "neproto-client %s\n", buildinfo.Version)
		return 0
	}
	if len(arguments) == 0 || (arguments[0] != "run" && arguments[0] != "check" && arguments[0] != "probe") {
		clientUsage(stderr)
		return 2
	}
	command := arguments[0]
	flags := flag.NewFlagSet("neproto-client "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to strict JSON configuration")
	carrierMode := flags.String("carrier", "auto", "probe carrier: auto, http3, webrtc, or https")
	if err := flags.Parse(arguments[1:]); err != nil || *configPath == "" || flags.NArg() != 0 {
		clientUsage(stderr)
		return 2
	}
	loaded, err := config.LoadClient(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "client configuration error: %v\n", err)
		return 1
	}
	if command == "check" {
		if *carrierMode != "auto" {
			clientUsage(stderr)
			return 2
		}
		fmt.Fprintln(stdout, "client configuration OK")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if command == "probe" {
		mode, err := parseProbeMode(*carrierMode)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		probeTimeout := loaded.WebRTCTimeout.Duration + loaded.HTTPSTimeout.Duration + 10*time.Second
		if loaded.HTTP3Configured() {
			probeTimeout += loaded.HTTP3Timeout.Duration
		}
		probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		result, err := app.ProbeClient(probeContext, loaded, mode)
		if err != nil {
			fmt.Fprintf(stderr, "client probe failed: %v\n", err)
			return 1
		}
		writeProbeResult(stdout, result)
		return 0
	}
	if *carrierMode != "auto" {
		clientUsage(stderr)
		return 2
	}
	if err := app.RunClient(ctx, loaded); err != nil {
		fmt.Fprintf(stderr, "client stopped: %v\n", err)
		return 1
	}
	return 0
}

func writeProbeResult(writer io.Writer, result app.ProbeResult) {
	kind := "https"
	switch result.Kind {
	case protocol.CarrierWebRTC:
		kind = "webrtc"
	case protocol.CarrierHTTP3:
		kind = "http3"
	}
	fmt.Fprintf(writer, "carrier=%s fallback=%t authentication=ok\n", kind, result.UsedFallback)
	mode := "fixed"
	if result.MosaicEnabled {
		mode = "mosaic"
	}
	fmt.Fprintf(
		writer, "cover=%s class=%s transitions=%d\n",
		mode, result.CoverClass, result.CoverTransitions,
	)
	fmt.Fprintf(
		writer,
		"mosaic_variant=%d bursts=%d dummy_selected=%d dummy_rejected=%d added_delay_us=%d max_delay_us=%d\n",
		result.CoverVariantID, result.CoverBurstCount, result.CoverDummySelected,
		result.CoverDummyRejected, result.CoverAddedDelayMicros, result.CoverMaxDelayMicros,
	)
	fmt.Fprintf(
		writer, "constellation=%t forward_secrecy=%t\n",
		result.ConstellationEnabled, result.ForwardSecrecyEnabled,
	)
}

func clientUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: neproto-client <run|check> --config <path> | probe --config <path> [--carrier auto|http3|webrtc|https] | version")
}

func parseProbeMode(value string) (app.ProbeMode, error) {
	switch strings.ToLower(value) {
	case "auto":
		return app.ProbeAuto, nil
	case "webrtc":
		return app.ProbeWebRTC, nil
	case "https":
		return app.ProbeHTTPS, nil
	case "http3":
		return app.ProbeHTTP3, nil
	default:
		return 0, fmt.Errorf("invalid probe carrier %q", value)
	}
}
