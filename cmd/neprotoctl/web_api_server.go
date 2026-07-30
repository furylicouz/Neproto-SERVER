package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const defaultWebAPIControlSocket = "/run/neproto/control.sock"

func runWebAPIServer(arguments []string, root string, stdout, stderr io.Writer, controller serviceController) int {
	flags := flag.NewFlagSet("web-api-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := flags.String("socket", defaultWebAPIControlSocket, "local Unix control socket")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return usage(stderr)
	}
	cleanSocket := filepath.Clean(*socket)
	if !filepath.IsAbs(cleanSocket) || (root == "/" && filepath.Dir(cleanSocket) != "/run/neproto") {
		fmt.Fprintln(stderr, "web API socket must be an absolute path in /run/neproto")
		return 2
	}
	handler, err := newWebAPIHandler(root, controller)
	if err != nil {
		fmt.Fprintf(stderr, "cannot initialize web API: %v\n", err)
		return 1
	}
	if err := prepareWebAPISocket(cleanSocket); err != nil {
		fmt.Fprintf(stderr, "cannot prepare web API socket: %v\n", err)
		return 1
	}
	listener, err := net.Listen("unix", cleanSocket)
	if err != nil {
		fmt.Fprintf(stderr, "cannot listen on web API socket: %v\n", err)
		return 1
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(cleanSocket)
	}()
	if err := os.Chmod(cleanSocket, 0o660); err != nil {
		fmt.Fprintf(stderr, "cannot secure web API socket: %v\n", err)
		return 1
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      20 * time.Minute,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	fmt.Fprintf(stdout, "NeProto web control API listening on %s\n", cleanSocket)
	serverContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveResult:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "web API server failed: %v\n", err)
			return 1
		}
	case <-serverContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			fmt.Fprintf(stderr, "web API shutdown failed: %v\n", err)
			return 1
		}
	}
	return 0
}

func prepareWebAPISocket(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o770); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("refusing to replace non-socket control path")
	}
	return os.Remove(path)
}
