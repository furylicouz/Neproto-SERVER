package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sort"
	"time"

	"neproto.local/chameleon/internal/admin"
)

func domainCommand(
	manager *admin.Manager,
	controller serviceController,
	arguments []string,
	stdout, stderr io.Writer,
) int {
	if len(arguments) == 0 || arguments[0] != "set" {
		return usage(stderr)
	}
	flags := flag.NewFlagSet("domain set", flag.ContinueOnError)
	flags.SetOutput(stderr)
	domain := flags.String("domain", "", "new lowercase VPN domain")
	if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *domain == "" {
		return usage(stderr)
	}
	addresses, err := resolveDomainAddresses(*domain)
	if err != nil {
		fmt.Fprintf(stderr, "resolve new domain failed: %v\n", err)
		return 1
	}
	return performDomainSet(manager, controller, *domain, addresses, stdout, stderr)
}

func featureCommand(
	manager *admin.Manager,
	controller serviceController,
	arguments []string,
	stdout, stderr io.Writer,
) int {
	if len(arguments) == 0 || arguments[0] != "set" {
		return usage(stderr)
	}
	flags := flag.NewFlagSet("feature set", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "", "production or compatibility")
	if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 ||
		(*mode != "production" && *mode != "compatibility") {
		return usage(stderr)
	}
	return performFeatureSet(manager, controller, *mode == "production", stdout, stderr)
}

func performFeatureSet(
	manager *admin.Manager,
	controller serviceController,
	production bool,
	stdout, stderr io.Writer,
) int {
	installation := manager.Installation()
	if installation.EnableConstellation == production &&
		installation.EnableForwardSecrecy == production {
		if production {
			fmt.Fprintln(stdout, "Production mode is already enabled.")
		} else {
			fmt.Fprintln(stdout, "Compatibility mode is already enabled.")
		}
		return 0
	}
	backup, err := manager.CreateBackup()
	if err != nil {
		fmt.Fprintf(stderr, "create feature rollback backup failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Rollback backup: %s\n", backup)
	if err := manager.SetFeatures(production, production); err != nil {
		fmt.Fprintf(stderr, "update feature policy failed: %v\n", err)
		return 1
	}
	if err := controller.Validate(io.Discard, stderr); err != nil {
		return rollbackFeatureChange(manager, controller, backup, err, stdout, stderr)
	}
	if err := controller.Action("restart", stdout, stderr); err != nil {
		return rollbackFeatureChange(manager, controller, backup, err, stdout, stderr)
	}
	if err := controller.PublicProbe(manager.Installation()); err != nil {
		return rollbackFeatureChange(manager, controller, backup, err, stdout, stderr)
	}
	if production {
		fmt.Fprintln(stdout, "Production mode enabled: Constellation continuity and forward secrecy are active.")
	} else {
		fmt.Fprintln(stdout, "Compatibility mode enabled: Constellation continuity and forward secrecy are disabled.")
	}
	fmt.Fprintln(stdout, "Re-export client profiles so their negotiated policy matches the server.")
	return 0
}

func rollbackFeatureChange(
	manager *admin.Manager,
	controller serviceController,
	backup string,
	cause error,
	stdout, stderr io.Writer,
) int {
	fmt.Fprintf(stderr, "feature policy failed, rolling back: %v\n", cause)
	_, restoreErr := manager.RestoreBackup(backup)
	validationErr := controller.Validate(io.Discard, stderr)
	restartErr := controller.Action("restart", stdout, stderr)
	if err := errors.Join(restoreErr, validationErr, restartErr); err != nil {
		fmt.Fprintf(stderr, "FEATURE ROLLBACK FAILED: %v\n", err)
	} else {
		fmt.Fprintln(stderr, "Previous feature policy restored.")
	}
	return 1
}

func performDomainSet(
	manager *admin.Manager,
	controller serviceController,
	domain string,
	addresses []string,
	stdout, stderr io.Writer,
) int {
	if domain == manager.Installation().Domain {
		fmt.Fprintln(stderr, "new domain is already configured")
		return 1
	}
	backup, err := manager.CreateBackup()
	if err != nil {
		fmt.Fprintf(stderr, "create domain rollback backup failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Rollback backup: %s\n", backup)
	if err := manager.SetDomain(domain, addresses); err != nil {
		fmt.Fprintf(stderr, "update domain failed: %v\n", err)
		return 1
	}
	if err := controller.ProvisionCertificate(domain, stdout, stderr); err != nil {
		return rollbackDomainChange(manager, controller, backup, err, stdout, stderr)
	}
	if err := controller.Validate(io.Discard, stderr); err != nil {
		return rollbackDomainChange(manager, controller, backup, fmt.Errorf("configuration validation: %w", err), stdout, stderr)
	}
	if err := controller.Action("restart", stdout, stderr); err != nil {
		return rollbackDomainChange(manager, controller, backup, fmt.Errorf("service restart: %w", err), stdout, stderr)
	}
	if err := controller.PublicProbe(manager.Installation()); err != nil {
		return rollbackDomainChange(manager, controller, backup, fmt.Errorf("public readiness: %w", err), stdout, stderr)
	}
	fmt.Fprintf(stdout, "Domain changed to %s\n", domain)
	fmt.Fprintln(stdout, "Re-export every client profile because the authenticated server identity changed.")
	return 0
}

func rollbackDomainChange(
	manager *admin.Manager,
	controller serviceController,
	backup string,
	cause error,
	stdout, stderr io.Writer,
) int {
	fmt.Fprintf(stderr, "domain change failed, rolling back: %v\n", cause)
	_, restoreErr := manager.RestoreBackup(backup)
	certificateErr := error(nil)
	if restoreErr == nil {
		certificateErr = controller.ProvisionCertificate(manager.Installation().Domain, stdout, stderr)
	}
	validationErr := controller.Validate(io.Discard, stderr)
	restartErr := controller.Action("restart", stdout, stderr)
	if err := errors.Join(restoreErr, certificateErr, validationErr, restartErr); err != nil {
		fmt.Fprintf(stderr, "ROLLBACK FAILED: %v\n", err)
	} else {
		fmt.Fprintln(stderr, "Previous domain configuration restored.")
	}
	return 1
}

func backupCommand(
	manager *admin.Manager,
	controller serviceController,
	arguments []string,
	stdout, stderr io.Writer,
) int {
	if len(arguments) == 0 {
		return usage(stderr)
	}
	switch arguments[0] {
	case "create":
		if len(arguments) != 1 {
			return usage(stderr)
		}
		path, err := manager.CreateBackup()
		if err != nil {
			fmt.Fprintf(stderr, "backup failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Backup created: %s\n", path)
		return 0
	case "list":
		if len(arguments) != 1 {
			return usage(stderr)
		}
		paths, err := manager.ListBackups()
		if err != nil {
			fmt.Fprintf(stderr, "list backups failed: %v\n", err)
			return 1
		}
		if len(paths) == 0 {
			fmt.Fprintln(stdout, "No backups found.")
			return 0
		}
		for position, path := range paths {
			fmt.Fprintf(stdout, "%d. %s\n", position+1, path)
		}
		return 0
	case "restore":
		flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
		flags.SetOutput(stderr)
		path := flags.String("path", "", "backup directory")
		confirmation := flags.String("confirm", "", "must be RESTORE")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 ||
			*path == "" || *confirmation != "RESTORE" {
			return usage(stderr)
		}
		recovery, err := manager.RestoreBackup(*path)
		if err != nil {
			fmt.Fprintf(stderr, "restore failed: %v\n", err)
			return 1
		}
		if err := controller.ProvisionCertificate(manager.Installation().Domain, stdout, stderr); err != nil {
			_, _ = manager.RestoreBackup(recovery)
			_ = controller.ProvisionCertificate(manager.Installation().Domain, stdout, stderr)
			fmt.Fprintf(stderr, "restored certificate provisioning failed; recovery restored: %v\n", err)
			return 1
		}
		if err := controller.Validate(io.Discard, stderr); err != nil {
			_, _ = manager.RestoreBackup(recovery)
			fmt.Fprintf(stderr, "restored configuration is invalid; recovery restored: %v\n", err)
			return 1
		}
		if err := controller.Action("restart", stdout, stderr); err != nil {
			_, _ = manager.RestoreBackup(recovery)
			_ = controller.Action("restart", stdout, stderr)
			fmt.Fprintf(stderr, "restored services failed; recovery restored: %v\n", err)
			return 1
		}
		if err := controller.PublicProbe(manager.Installation()); err != nil {
			_, _ = manager.RestoreBackup(recovery)
			_ = controller.Action("restart", stdout, stderr)
			fmt.Fprintf(stderr, "restored public service failed; recovery restored: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Backup restored. Pre-restore recovery: %s\n", recovery)
		return 0
	default:
		return usage(stderr)
	}
}

var resolveDomainAddresses = func(domain string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	resolved, err := net.DefaultResolver.LookupNetIP(ctx, "ip", domain)
	if err != nil {
		return nil, err
	}
	unique := make(map[netip.Addr]struct{}, len(resolved))
	for _, address := range resolved {
		address = address.Unmap()
		if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
			address.IsLoopback() || address.IsLinkLocalUnicast() {
			continue
		}
		unique[address] = struct{}{}
	}
	addresses := make([]string, 0, len(unique))
	for address := range unique {
		addresses = append(addresses, address.String())
	}
	sort.Strings(addresses)
	if len(addresses) == 0 || len(addresses) > 8 {
		return nil, fmt.Errorf("domain must resolve to 1-8 public addresses")
	}
	return addresses, nil
}
