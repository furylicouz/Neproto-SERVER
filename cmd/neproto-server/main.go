package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 1 && arguments[0] == "version" {
		fmt.Fprintf(stdout, "neproto-server %s\n", buildinfo.Version)
		return 0
	}
	if len(arguments) == 1 && arguments[0] == "generate-secret" {
		secret, err := config.GenerateSecret(rand.Reader)
		if err != nil {
			fmt.Fprintf(stderr, "secret generation failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, secret)
		return 0
	}
	if len(arguments) >= 1 && arguments[0] == "cluster-attest" {
		return clusterAttestation(arguments[1:], stdout, stderr)
	}
	if len(arguments) == 0 || (arguments[0] != "run" && arguments[0] != "check") {
		serverUsage(stderr)
		return 2
	}
	command := arguments[0]
	flags := flag.NewFlagSet("neproto-server "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to strict JSON configuration")
	if err := flags.Parse(arguments[1:]); err != nil || *configPath == "" || flags.NArg() != 0 {
		serverUsage(stderr)
		return 2
	}
	loaded, err := config.LoadServer(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "server configuration error: %v\n", err)
		return 1
	}
	if command == "check" {
		fmt.Fprintln(stdout, "server configuration OK")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.RunServer(ctx, loaded); err != nil {
		fmt.Fprintf(stderr, "server stopped: %v\n", err)
		return 1
	}
	return 0
}

func serverUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: neproto-server <run|check> --config <path> | cluster-attest [--state path] [--format token|json] | generate-secret | version")
}
