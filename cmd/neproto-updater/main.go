package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/selfupdate"
)

const (
	defaultStateDirectory = "/var/lib/neproto/update"
	staleLockAge          = 45 * time.Minute
	applyLockWait         = 90 * time.Second
)

var errUpdateBusy = errors.New("another update operation is active")

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "neproto-updater: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) != 1 || (arguments[0] != "check" && arguments[0] != "apply") {
		return errors.New("usage: neproto-updater check|apply")
	}
	root := strings.TrimSpace(os.Getenv("NEPROTO_TEST_ROOT"))
	if root == "" {
		root = string(filepath.Separator)
	}
	stateDirectory := rooted(root, defaultStateDirectory)
	action := arguments[0]
	if action == "check" {
		_ = os.Remove(filepath.Join(stateDirectory, "inbox", "check"))
	}
	wait := time.Duration(0)
	if action == "apply" {
		wait = applyLockWait
	}
	release, err := acquireLockWithWait(stateDirectory, wait)
	if err != nil {
		if action == "check" && errors.Is(err, errUpdateBusy) {
			return nil
		}
		return err
	}
	defer release()

	duration := 45 * time.Second
	if action == "apply" {
		duration = 30 * time.Minute
		_ = os.Remove(filepath.Join(stateDirectory, "inbox", "apply"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	engine := selfupdate.NewEngine(buildinfo.Version, root, stateDirectory)
	if action == "check" {
		_, err = engine.Check(ctx)
		return err
	}
	_, err = engine.Apply(ctx)
	if errors.Is(err, selfupdate.ErrNoUpdate) {
		return nil
	}
	return err
}

func rooted(root, absolute string) string {
	if filepath.Clean(root) == string(filepath.Separator) {
		return filepath.Clean(absolute)
	}
	return filepath.Join(filepath.Clean(root), strings.TrimLeft(absolute, `/\`))
}

func acquireLock(stateDirectory string) (func(), error) {
	return acquireLockWithWait(stateDirectory, 0)
}

func acquireLockWithWait(stateDirectory string, maximumWait time.Duration) (func(), error) {
	if err := os.MkdirAll(stateDirectory, 0o750); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(stateDirectory, "updater.lock")
	deadline := time.Now().Add(maximumWait)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, writeErr := fmt.Fprintf(file, "%d\n", time.Now().UTC().Unix())
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = os.Remove(lockPath)
				return nil, errors.Join(writeErr, closeErr)
			}
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		encoded, readErr := os.ReadFile(lockPath)
		if readErr != nil || len(encoded) > 64 {
			return nil, errUpdateBusy
		}
		started, parseErr := strconv.ParseInt(strings.TrimSpace(string(encoded)), 10, 64)
		if parseErr == nil && time.Since(time.Unix(started, 0)) > staleLockAge {
			if err := os.Remove(lockPath); err != nil {
				return nil, errors.New("cannot clear stale update lock")
			}
			continue
		}
		if maximumWait <= 0 || !time.Now().Before(deadline) {
			return nil, errUpdateBusy
		}
		remaining := time.Until(deadline)
		pause := 250 * time.Millisecond
		if remaining < pause {
			pause = remaining
		}
		time.Sleep(pause)
	}
}
