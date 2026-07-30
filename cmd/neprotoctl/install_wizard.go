package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

type installStage uint8

const (
	installStageMode installStage = iota
	installStageDomain
	installStageWebDomain
	installStageEmail
	installStageConfirm
	installStageRunning
	installStageDone
	installStageFailed
)

type installWizardModel struct {
	stage     installStage
	mode      string
	domain    string
	webDomain string
	webPort   int
	email     string
	input     string
	errorText string
	logs      []string
	progress  int
	startedAt time.Time
}

type installWizardLogEvent struct{ line string }
type installWizardDoneEvent struct{ err error }

func newInstallWizardModel() installWizardModel {
	return installWizardModel{stage: installStageMode, mode: "bare-metal", webPort: 3000, progress: 3}
}

func installWizardCommand(arguments []string, stdout, stderr *os.File) int {
	flags := flag.NewFlagSet("install-wizard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	script := flags.String("script", "", "absolute path to the bundled install.sh")
	skipStart := flags.Bool("skip-start", false, "prepare installation without starting services")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 || *script == "" {
		fmt.Fprintln(stderr, "usage: neprotoctl install-wizard --script /absolute/path/install.sh [--skip-start]")
		return 2
	}
	cleanScript, err := validateInstallScript(*script)
	if err != nil {
		fmt.Fprintf(stderr, "invalid installer script: %v\n", err)
		return 2
	}
	if !terminalInput(os.Stdin) || !terminalOutput(stdout) {
		fmt.Fprintln(stderr, "the Constellation installer requires an interactive terminal")
		return 2
	}
	code, err := runInstallWizardTUI(cleanScript, *skipStart)
	if err != nil {
		fmt.Fprintf(stderr, "installer console failed: %v\n", err)
		return 1
	}
	return code
}

func validateInstallScript(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || filepath.Base(resolved) != "install.sh" {
		return "", errors.New("expected a regular install.sh file")
	}
	return resolved, nil
}

func runInstallWizardTUI(script string, skipStart bool) (int, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return 1, fmt.Errorf("create screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return 1, fmt.Errorf("initialize screen: %w", err)
	}
	defer screen.Fini()
	screen.HideCursor()
	model := newInstallWizardModel()
	renderInstallWizard(screen, &model)

	for {
		event := screen.PollEvent()
		switch event := event.(type) {
		case *tcell.EventResize:
			screen.Sync()
		case *tcell.EventKey:
			quit, start := handleInstallWizardKey(event, &model)
			if quit {
				if model.stage == installStageDone {
					return 0, nil
				}
				return 1, nil
			}
			if start {
				model.stage = installStageRunning
				model.progress = 5
				model.startedAt = time.Now()
				model.logs = append(model.logs, "Deployment transaction started")
				startInstallScript(screen, script, model, skipStart)
			}
		case *tcell.EventInterrupt:
			switch data := event.Data().(type) {
			case installWizardLogEvent:
				model.logs = appendBoundedInstallLog(model.logs, data.line, 500)
				model.progress = maxInt(model.progress, installProgressForLine(data.line))
			case installWizardDoneEvent:
				if data.err != nil {
					model.stage = installStageFailed
					model.errorText = boundedDisplay(data.err.Error(), 240)
					model.logs = appendBoundedInstallLog(model.logs, "FAILED: "+data.err.Error(), 500)
				} else {
					model.stage = installStageDone
					model.progress = 100
					model.logs = appendBoundedInstallLog(model.logs, "Installation verified. Run np to manage the server.", 500)
				}
			}
		case nil:
			return 1, errors.New("terminal event stream closed")
		}
		renderInstallWizard(screen, &model)
	}
}

func startInstallScript(screen tcell.Screen, script string, model installWizardModel, skipStart bool) {
	arguments := installScriptArguments(model, skipStart)
	command := exec.Command("bash", append([]string{script}, arguments...)...)
	command.Env = append(os.Environ(), "NEPROTO_CLASSIC_INSTALL=1")
	reader, writer := ioPipe()
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Start(); err != nil {
		_ = writer.Close()
		_ = screen.PostEvent(tcell.NewEventInterrupt(installWizardDoneEvent{err: err}))
		return
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
		_ = writer.Close()
	}()
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			screen.PostEventWait(tcell.NewEventInterrupt(installWizardLogEvent{line: scanner.Text()}))
		}
		waitErr := <-done
		_ = reader.Close()
		screen.PostEventWait(tcell.NewEventInterrupt(installWizardDoneEvent{err: errors.Join(waitErr, scanner.Err())}))
	}()
}

// ioPipe is a small seam kept separate so the process runner never invokes a
// shell with user-controlled text. Values are passed as fixed argv entries.
func ioPipe() (*io.PipeReader, *io.PipeWriter) {
	return io.Pipe()
}

func installScriptArguments(model installWizardModel, skipStart bool) []string {
	arguments := []string{"--mode", model.mode, "--domain", model.domain}
	if model.webDomain != "" {
		arguments = append(arguments, "--web-domain", model.webDomain)
	}
	arguments = append(arguments, "--web-port", fmt.Sprintf("%d", model.webPort))
	if model.email != "" {
		arguments = append(arguments, "--email", model.email)
	}
	arguments = append(arguments, "--non-interactive")
	if skipStart {
		arguments = append(arguments, "--skip-start")
	}
	return arguments
}

func handleInstallWizardKey(event *tcell.EventKey, model *installWizardModel) (quit, start bool) {
	if model.stage == installStageRunning {
		return false, false
	}
	if model.stage == installStageDone || model.stage == installStageFailed {
		if event.Key() == tcell.KeyEnter || event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyCtrlC {
			return true, false
		}
		return false, false
	}
	if event.Key() == tcell.KeyCtrlC {
		return true, false
	}
	if event.Key() == tcell.KeyEscape {
		switch model.stage {
		case installStageMode:
			return true, false
		case installStageDomain:
			model.stage = installStageMode
			model.input = ""
		case installStageWebDomain:
			model.stage = installStageDomain
			model.input = model.domain
		case installStageEmail:
			model.stage = installStageWebDomain
			model.input = model.webDomain
		case installStageConfirm:
			model.stage = installStageEmail
			model.input = model.email
		}
		model.errorText = ""
		return false, false
	}
	if model.stage == installStageMode {
		switch event.Key() {
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
			if model.mode == "bare-metal" {
				model.mode = "docker"
			} else {
				model.mode = "bare-metal"
			}
		case tcell.KeyEnter:
			model.stage = installStageDomain
			model.input = model.domain
		}
		return false, false
	}
	if model.stage == installStageConfirm {
		if event.Key() == tcell.KeyEnter {
			return false, true
		}
		return false, false
	}
	if event.Key() == tcell.KeyBackspace || event.Key() == tcell.KeyBackspace2 {
		if runes := []rune(model.input); len(runes) > 0 {
			model.input = string(runes[:len(runes)-1])
		}
		model.errorText = ""
		return false, false
	}
	if event.Key() == tcell.KeyRune && !unicode.IsControl(event.Rune()) && len([]rune(model.input)) < 254 {
		model.input += string(event.Rune())
		model.errorText = ""
		return false, false
	}
	if event.Key() != tcell.KeyEnter {
		return false, false
	}
	if model.stage == installStageDomain {
		candidate := strings.TrimSpace(model.input)
		if err := validateInstallDomain(candidate); err != nil {
			model.errorText = err.Error()
			return false, false
		}
		model.domain = candidate
		model.input = model.webDomain
		model.stage = installStageWebDomain
		model.progress = 6
		return false, false
	}
	if model.stage == installStageWebDomain {
		candidate := strings.TrimSpace(model.input)
		if err := validateInstallWebDomain(candidate, model.domain); err != nil {
			model.errorText = err.Error()
			return false, false
		}
		if err := validateInstallWebPort(model.webPort); err != nil {
			model.errorText = err.Error()
			return false, false
		}
		model.webDomain = candidate
		model.input = model.email
		model.stage = installStageEmail
		model.progress = 7
		return false, false
	}
	candidate := strings.TrimSpace(model.input)
	if err := validateInstallEmail(candidate); err != nil {
		model.errorText = err.Error()
		return false, false
	}
	model.email = candidate
	model.input = ""
	model.stage = installStageConfirm
	model.progress = 8
	return false, false
}

func validateInstallDomain(domain string) error {
	if domain == "" || len(domain) > 253 || domain != strings.ToLower(domain) || !strings.Contains(domain, ".") || strings.Contains(domain, "..") {
		return errors.New("enter a lowercase DNS domain such as vpn.example.com")
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("domain contains an invalid DNS label")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return errors.New("domain may contain only lowercase letters, digits, dots, and hyphens")
			}
		}
	}
	return nil
}

func validateInstallEmail(email string) error {
	if email == "" {
		return nil
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.ContainsAny(email, " \t\r\n") {
		return errors.New("enter a valid ACME email or leave the field empty")
	}
	parts := strings.Split(address.Address, "@")
	if len(parts) != 2 || parts[0] == "" || validateInstallDomain(parts[1]) != nil {
		return errors.New("ACME email must use a fully qualified domain")
	}
	return nil
}

func validateInstallWebDomain(webDomain, serverDomain string) error {
	if webDomain == "" {
		return nil
	}
	if webDomain == serverDomain {
		return errors.New("web domain must differ from the NP/2 server domain")
	}
	if err := validateInstallDomain(webDomain); err != nil {
		return fmt.Errorf("web domain: %w", err)
	}
	return nil
}

func validateInstallWebPort(port int) error {
	if port < 1024 || port > 65535 || port == 9080 || port == 9464 || (port >= 40000 && port <= 40100) {
		return errors.New("web port must be 1024-65535 and not overlap NP/2 service ports")
	}
	return nil
}

func appendBoundedInstallLog(lines []string, line string, maximum int) []string {
	line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
	if line == "" {
		return lines
	}
	lines = append(lines, boundedDisplay(line, 512))
	if len(lines) > maximum {
		return append([]string(nil), lines[len(lines)-maximum:]...)
	}
	return lines
}

func installProgressForLine(line string) int {
	lower := strings.ToLower(line)
	for _, marker := range []struct {
		text     string
		progress int
	}{
		{"checking host", 8}, {"installing system dependencies", 18}, {"preparing secure directories", 32},
		{"writing np/2 configuration", 48}, {"installing runtime", 58}, {"provisioning tls", 72},
		{"starting services", 86}, {"running final health checks", 95}, {"installation prepared", 99},
	} {
		if strings.Contains(lower, marker.text) {
			return marker.progress
		}
	}
	return 0
}
