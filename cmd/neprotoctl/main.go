package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/onboarding"
)

type serviceController interface {
	Action(string, io.Writer, io.Writer) error
	Logs(bool, io.Writer, io.Writer) error
	Snapshot() serviceSnapshot
	Validate(io.Writer, io.Writer) error
	PublicProbe(admin.Installation) error
	ProvisionCertificate(string, io.Writer, io.Writer) error
}

type serviceSnapshot struct {
	NP2     string
	Ingress string
	Web     string
}

type commandController struct {
	mode string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install-wizard" {
		os.Exit(installWizardCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
	root := "/"
	var controller serviceController
	if testingRoot := os.Getenv("NEPROTO_TEST_ROOT"); testingRoot != "" {
		root = testingRoot
		controller = noServiceController{}
	}
	os.Exit(executeWithInput(os.Args[1:], root, os.Stdin, os.Stdout, os.Stderr, controller))
}

type noServiceController struct{}

func (noServiceController) Action(string, io.Writer, io.Writer) error { return nil }
func (noServiceController) Logs(bool, io.Writer, io.Writer) error     { return nil }
func (noServiceController) Snapshot() serviceSnapshot {
	return serviceSnapshot{NP2: "test", Ingress: "test", Web: "test"}
}
func (noServiceController) Validate(io.Writer, io.Writer) error                     { return nil }
func (noServiceController) PublicProbe(admin.Installation) error                    { return nil }
func (noServiceController) ProvisionCertificate(string, io.Writer, io.Writer) error { return nil }

func execute(arguments []string, root string, stdout, stderr io.Writer, controller serviceController) int {
	return executeWithInput(arguments, root, strings.NewReader(""), stdout, stderr, controller)
}

func executeWithInput(
	arguments []string,
	root string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	controller serviceController,
) int {
	if len(arguments) == 0 || arguments[0] == "menu" {
		return menu(root, stdin, stdout, stderr, controller)
	}
	if len(arguments) == 1 && arguments[0] == "version" {
		fmt.Fprintf(stdout, "neprotoctl %s\n", buildinfo.Version)
		return 0
	}
	manager, err := admin.Open(root, nil, nil)
	if err != nil {
		fmt.Fprintf(stderr, "cannot open NP/2 installation: %v\n", err)
		return 1
	}
	if controller == nil {
		controller = commandController{mode: manager.Installation().Mode}
	}

	switch arguments[0] {
	case "user":
		return userCommand(manager, controller, arguments[1:], stdout, stderr)
	case "status":
		if len(arguments) != 1 {
			return usage(stderr)
		}
		if err := controller.Action("status", stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "status failed: %v\n", err)
			return 1
		}
		return 0
	case "doctor":
		if len(arguments) != 1 {
			return usage(stderr)
		}
		return runDoctor(manager, controller, stdout, stderr)
	case "domain":
		return domainCommand(manager, controller, arguments[1:], stdout, stderr)
	case "backup":
		return backupCommand(manager, controller, arguments[1:], stdout, stderr)
	case "feature":
		return featureCommand(manager, controller, arguments[1:], stdout, stderr)
	case "geodata":
		return geodataCommand(manager, controller, arguments[1:], stdout, stderr)
	case "service":
		if len(arguments) != 2 || (arguments[1] != "start" && arguments[1] != "stop" && arguments[1] != "restart") {
			return usage(stderr)
		}
		if err := controller.Action(arguments[1], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "service action failed: %v\n", err)
			return 1
		}
		return 0
	case "logs":
		flags := flag.NewFlagSet("logs", flag.ContinueOnError)
		flags.SetOutput(stderr)
		follow := flags.Bool("follow", false, "follow service logs")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 {
			return usage(stderr)
		}
		if err := controller.Logs(*follow, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "logs failed: %v\n", err)
			return 1
		}
		return 0
	default:
		return usage(stderr)
	}
}

func userCommand(manager *admin.Manager, controller serviceController, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		return usage(stderr)
	}
	switch arguments[0] {
	case "add":
		flags := flag.NewFlagSet("user add", flag.ContinueOnError)
		flags.SetOutput(stderr)
		name := flags.String("name", "", "device/user name")
		profile := flags.String("profile", "web", "quiet, web, or interactive")
		noRestart := flags.Bool("no-restart", false, "defer server restart (installer use)")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *name == "" {
			return usage(stderr)
		}
		user, err := manager.AddUser(*name, *profile)
		if err != nil {
			fmt.Fprintf(stderr, "create user failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Created %s (%s)\n", user.Name, user.ID)
		if *noRestart {
			return 0
		}
		return restartAfterCredentialChange(controller, stdout, stderr)
	case "list":
		if len(arguments) != 1 {
			return usage(stderr)
		}
		users, err := manager.ListUsers()
		if err != nil {
			fmt.Fprintf(stderr, "list users failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "ID\tSTATUS\tPROFILE\tNAME")
		for _, user := range users {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", user.ID, user.Status, user.Profile, user.Name)
		}
		return 0
	case "export":
		flags := flag.NewFlagSet("user export", flag.ContinueOnError)
		flags.SetOutput(stderr)
		identifier := flags.String("id", "", "credential ID")
		format := flags.String("format", "qr", "manual, uri, json, qr, or png")
		output := flags.String("output", "", "output path for png/json")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *identifier == "" {
			return usage(stderr)
		}
		uri, err := manager.ExportUserURI(*identifier)
		if err != nil {
			fmt.Fprintf(stderr, "export user failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(stderr, "WARNING: this QR/URI is a bearer credential; show it only to the intended user.")
		if err := exportCredential(uri, *format, *output, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "export failed: %v\n", err)
			return 1
		}
		return 0
	case "rotate", "revoke":
		flags := flag.NewFlagSet("user "+arguments[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		identifier := flags.String("id", "", "credential ID")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *identifier == "" {
			return usage(stderr)
		}
		var err error
		if arguments[0] == "rotate" {
			err = manager.RotateUser(*identifier)
		} else {
			err = manager.RevokeUser(*identifier)
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s user failed: %v\n", arguments[0], err)
			return 1
		}
		if err := syncClusterUserCredential(manager, *identifier); err != nil {
			fmt.Fprintf(stderr, "%s completed locally, but cluster sync failed: %v\n", arguments[0], err)
			return 1
		}
		fmt.Fprintf(stdout, "User %s completed for %s\n", arguments[0], *identifier)
		return restartAfterCredentialChange(controller, stdout, stderr)
	case "delete":
		flags := flag.NewFlagSet("user delete", flag.ContinueOnError)
		flags.SetOutput(stderr)
		identifier := flags.String("id", "", "revoked credential ID")
		confirmation := flags.String("confirm", "", "must be DELETE")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *identifier == "" || *confirmation != "DELETE" {
			return usage(stderr)
		}
		if err := manager.DeleteUser(*identifier); err != nil {
			fmt.Fprintf(stderr, "delete user failed: %v\n", err)
			return 1
		}
		if err := syncClusterUserCredential(manager, *identifier); err != nil {
			fmt.Fprintf(stderr, "user deleted locally, but cluster sync failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Permanently deleted revoked user %s\n", *identifier)
		return restartAfterCredentialChange(controller, stdout, stderr)
	default:
		return usage(stderr)
	}
}

func restartAfterCredentialChange(controller serviceController, stdout, stderr io.Writer) int {
	if err := controller.Action("restart", stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "credential state changed, but server restart failed: %v\n", err)
		return 1
	}
	return 0
}

func exportCredential(uri, format, output string, stdout, stderr io.Writer) error {
	switch format {
	case "manual":
		if output != "" {
			return errors.New("--output is not used with manual terminal settings")
		}
		profile, err := onboarding.DecodeURI(uri)
		if err != nil {
			return err
		}
		writeManualProfile(stdout, profile)
		fmt.Fprintf(stdout, "Import URI: %s\n", uri)
		return nil
	case "uri":
		if output != "" {
			return writeSensitive(output, []byte(uri+"\n"))
		}
		fmt.Fprintln(stdout, uri)
		return nil
	case "json":
		profile, err := onboarding.DecodeURI(uri)
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(profile, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		if output != "" {
			return writeSensitive(output, raw)
		}
		_, err = stdout.Write(raw)
		return err
	case "qr":
		if output != "" {
			return errors.New("--output is not used with terminal QR")
		}
		return runQR([]string{"-t", "ANSIUTF8", "-o", "-"}, uri, stdout, stderr)
	case "png":
		if output == "" {
			return errors.New("--output is required for PNG")
		}
		return writeQRPNG(output, uri, stderr)
	default:
		return errors.New("format must be manual, uri, json, qr, or png")
	}
}

func writeManualProfile(output io.Writer, profile onboarding.Profile) {
	fmt.Fprintf(output, "Name: %s\n", profile.Name)
	fmt.Fprintf(output, "Server: %s\n", profile.ServerIdentity)
	fmt.Fprintf(output, "Addresses: %s\n", strings.Join(profile.ServerAddresses, ", "))
	fmt.Fprintf(output, "Credential ID: %s\n", profile.CredentialID)
	fmt.Fprintf(output, "Secret: %s\n", profile.Secret)
	fmt.Fprintf(output, "Profile: %s\n", profile.Profile)
	fmt.Fprintf(output, "HTTPS path: %s\n", profile.HTTPSPath)
	fmt.Fprintf(output, "WebRTC path: %s\n", profile.WebRTCPath)
	if profile.HTTP3Path != "" {
		fmt.Fprintf(output, "HTTP/3 path: %s\n", profile.HTTP3Path)
	}
	fmt.Fprintf(output, "Parallel carriers: %d\n", profile.MaxParallelCarriers)
	fmt.Fprintf(output, "Constellation: %t\n", profile.EnableConstellation)
	fmt.Fprintf(output, "Forward secrecy: %t\n", profile.EnableForwardSecrecy)
	if profile.ClusterID != "" {
		fmt.Fprintf(output, "Cluster ID: %s\n", profile.ClusterID)
		fmt.Fprintf(output, "Catalog public key: %s\n", profile.CatalogPublicKey)
	}
}

func runQR(arguments []string, uri string, stdout, stderr io.Writer) error {
	command := exec.Command("qrencode", arguments...)
	command.Stdin = strings.NewReader(uri)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("qrencode is required: %w", err)
	}
	return nil
}

func writeQRPNG(path, uri string, stderr io.Writer) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".np2-qr-*.png")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	_ = temporary.Close()
	defer os.Remove(temporaryPath)
	if err := runQR([]string{"-t", "PNG", "-o", temporaryPath}, uri, io.Discard, stderr); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func writeSensitive(path string, raw []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		return err
	}
	return file.Sync()
}

func (controller commandController) Action(action string, stdout, stderr io.Writer) error {
	var command *exec.Cmd
	if controller.mode == admin.ModeDocker {
		arguments := []string{"compose", "-f", "/opt/neproto/compose.yml"}
		switch action {
		case "status":
			arguments = append(arguments, "ps")
		case "start":
			arguments = append(arguments, "up", "-d")
		case "stop":
			arguments = append(arguments, "stop")
		case "restart":
			arguments = append(arguments, "restart", "neproto", "web", "caddy")
		case "restart-np2":
			arguments = append(arguments, "restart", "neproto")
		default:
			return errors.New("unsupported service action")
		}
		command = exec.Command("docker", arguments...)
	} else {
		if action == "status" {
			command = exec.Command("systemctl", "--no-pager", "status", "neproto-server.service", "neproto-web.service", "caddy.service")
		} else if action == "restart-np2" {
			command = exec.Command("systemctl", "restart", "neproto-server.service")
		} else {
			command = exec.Command("systemctl", action, "neproto-server.service", "neproto-web.service", "caddy.service")
		}
	}
	command.Stdout, command.Stderr = stdout, stderr
	return command.Run()
}

func (controller commandController) Logs(follow bool, stdout, stderr io.Writer) error {
	var command *exec.Cmd
	if controller.mode == admin.ModeDocker {
		arguments := []string{"compose", "-f", "/opt/neproto/compose.yml", "logs", "--tail", "200"}
		if follow {
			arguments = append(arguments, "--follow")
		}
		command = exec.Command("docker", arguments...)
	} else {
		arguments := []string{"-u", "neproto-server.service", "-u", "neproto-web.service", "-u", "caddy.service", "-n", "200", "--no-pager"}
		if follow {
			arguments = append(arguments, "-f")
		}
		command = exec.Command("journalctl", arguments...)
	}
	command.Stdout, command.Stderr = stdout, stderr
	return command.Run()
}

func (controller commandController) Validate(stdout, stderr io.Writer) error {
	commands := [][]string{}
	if controller.mode == admin.ModeDocker {
		commands = append(commands,
			[]string{"docker", "compose", "-f", "/opt/neproto/compose.yml", "config", "--quiet"},
			[]string{"docker", "compose", "-f", "/opt/neproto/compose.yml", "run", "--rm", "--no-deps", "neproto", "check", "--config", "/etc/neproto/server.json"},
			[]string{"docker", "compose", "-f", "/opt/neproto/compose.yml", "run", "--rm", "--no-deps", "web", "--check", "server.js"},
			[]string{"docker", "compose", "-f", "/opt/neproto/compose.yml", "run", "--rm", "--no-deps", "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"},
		)
	} else {
		commands = append(commands,
			[]string{"neproto-server", "check", "--config", "/etc/neproto/server.json"},
			[]string{"/usr/local/lib/neproto/node", "--check", "/opt/neproto/web/server.js"},
			[]string{"caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"},
		)
	}
	for _, definition := range commands {
		command := exec.Command(definition[0], definition[1:]...)
		command.Stdout, command.Stderr = stdout, stderr
		if err := command.Run(); err != nil {
			return err
		}
	}
	return nil
}

func (controller commandController) ProvisionCertificate(domain string, stdout, stderr io.Writer) error {
	command := exec.Command("/usr/local/lib/neproto/provision-certificate", "--domain", domain)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("provision certificate: %w", err)
	}
	return nil
}

func usage(writer io.Writer) int {
	fmt.Fprintln(writer, "usage: neprotoctl menu | status | doctor | service <start|stop|restart> | logs [--follow]")
	fmt.Fprintln(writer, "       neprotoctl user add --name NAME [--profile quiet|web|interactive]")
	fmt.Fprintln(writer, "       neprotoctl user delete --id ID --confirm DELETE")
	fmt.Fprintln(writer, "       neprotoctl user list")
	fmt.Fprintln(writer, "       neprotoctl user export --id ID [--format manual|uri|json|qr|png] [--output PATH]")
	fmt.Fprintln(writer, "       neprotoctl user rotate|revoke --id ID")
	fmt.Fprintln(writer, "       neprotoctl domain set --domain DOMAIN")
	fmt.Fprintln(writer, "       neprotoctl feature set --mode production|compatibility")
	fmt.Fprintln(writer, "       neprotoctl geodata status|update [--cluster=true|false] | geodata schedule --preset daily|weekly|monthly|off")
	fmt.Fprintln(writer, "       neprotoctl backup create|list | backup restore --path PATH --confirm RESTORE")
	return 2
}
