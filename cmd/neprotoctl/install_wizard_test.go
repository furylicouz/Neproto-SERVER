package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestInstallWizardCollectsValidatedDeploymentWithoutLeavingTUI(t *testing.T) {
	model := newInstallWizardModel()
	if model.stage != installStageMode || model.mode != "bare-metal" {
		t.Fatalf("initial model=%+v", model)
	}

	handleInstallWizardKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), &model)
	if model.mode != "docker" {
		t.Fatalf("mode=%q", model.mode)
	}
	handleInstallWizardKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	for _, character := range "vpn.example.com" {
		handleInstallWizardKey(tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone), &model)
	}
	handleInstallWizardKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	if model.stage != installStageWebDomain || model.domain != "vpn.example.com" {
		t.Fatalf("domain stage model=%+v", model)
	}
	for _, character := range "admin.example.com" {
		handleInstallWizardKey(tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone), &model)
	}
	handleInstallWizardKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	if model.stage != installStageEmail || model.webDomain != "admin.example.com" || model.webPort != 3000 {
		t.Fatalf("web stage model=%+v", model)
	}
	handleInstallWizardKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	if model.stage != installStageConfirm {
		t.Fatalf("stage=%v error=%q", model.stage, model.errorText)
	}
}

func TestInstallWizardRejectsUnsafeDomainAndEmail(t *testing.T) {
	for _, domain := range []string{"", "UPPER.example", "bad value.example", "-bad.example", "example"} {
		if validateInstallDomain(domain) == nil {
			t.Fatalf("domain %q accepted", domain)
		}
	}
	for _, email := range []string{"bad", "a b@example.com", "a@localhost"} {
		if validateInstallEmail(email) == nil {
			t.Fatalf("email %q accepted", email)
		}
	}
	if err := validateInstallEmail(""); err != nil {
		t.Fatalf("empty optional email: %v", err)
	}
	if err := validateInstallWebDomain("", "vpn.example.com"); err != nil {
		t.Fatalf("empty optional web domain: %v", err)
	}
	for _, webDomain := range []string{"vpn.example.com", "UPPER.example.com", "localhost"} {
		if validateInstallWebDomain(webDomain, "vpn.example.com") == nil {
			t.Fatalf("web domain %q accepted", webDomain)
		}
	}
	for _, port := range []int{0, 80, 443, 9080, 9464, 40000, 40100, 65536} {
		if validateInstallWebPort(port) == nil {
			t.Fatalf("reserved web port %d accepted", port)
		}
	}
	if err := validateInstallWebPort(3000); err != nil {
		t.Fatalf("web port 3000: %v", err)
	}
}

func TestInstallWizardRendersConstellationFrameAndProgressInsideScreen(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(132, 42)
	model := newInstallWizardModel()
	model.stage = installStageRunning
	model.mode = "docker"
	model.domain = "vpn.example.com"
	model.webDomain = "admin.example.com"
	model.webPort = 3000
	model.progress = 64
	model.logs = []string{"Installing Docker components", "Creating NP/2 configuration", "Validating services"}

	renderInstallWizard(screen, &model)
	content := simulationText(screen)
	for _, expected := range []string{"NEPROTO // CONSTELLATION DEPLOYMENT", "INSTALLATION MATRIX", "NETWORK MAP", "vpn.example.com", "admin.example.com", "64%", "Installing Docker components"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("installer TUI missing %q:\n%s", expected, content)
		}
	}
}

func TestInstallWizardCommandArgumentsAreFixedAndNotShellEvaluated(t *testing.T) {
	model := newInstallWizardModel()
	model.mode = "docker"
	model.domain = "vpn.example.com"
	model.webDomain = "admin.example.com"
	model.webPort = 3000
	model.email = "ops@example.com"
	arguments := installScriptArguments(model, true)
	want := []string{"--mode", "docker", "--domain", "vpn.example.com", "--web-domain", "admin.example.com", "--web-port", "3000", "--email", "ops@example.com", "--non-interactive", "--skip-start"}
	if strings.Join(arguments, "|") != strings.Join(want, "|") {
		t.Fatalf("arguments=%q want=%q", arguments, want)
	}
}
