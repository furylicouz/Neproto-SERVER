package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRootedKeepsRuntimePathsInsideTestRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	got := rooted(root, "/var/lib/neproto/update")
	want := filepath.Join(root, "var", "lib", "neproto", "update")
	if got != want {
		t.Fatalf("rooted = %q, want %q", got, want)
	}
}

func TestAcquireLockRejectsActiveOperationAndRecoversStaleLock(t *testing.T) {
	directory := t.TempDir()
	release, err := acquireLock(directory)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if _, err := acquireLock(directory); err == nil {
		t.Fatal("second updater acquired active lock")
	}
	release()

	lockPath := filepath.Join(directory, "updater.lock")
	if err := os.WriteFile(lockPath, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err = acquireLock(directory)
	if err != nil {
		t.Fatalf("recover stale lock: %v", err)
	}
	release()
}

func TestAcquireLockReportsStableBusyCategory(t *testing.T) {
	directory := t.TempDir()
	release, err := acquireLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := acquireLock(directory); !errors.Is(err, errUpdateBusy) {
		t.Fatalf("second lock error = %v, want errUpdateBusy", err)
	}
}

func TestScheduledCheckIsIdempotentlyConsumedWhileUpdaterIsBusy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NEPROTO_TEST_ROOT", root)
	stateDirectory := rooted(root, defaultStateDirectory)
	inbox := filepath.Join(stateDirectory, "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(inbox, "check")
	if err := os.WriteFile(marker, []byte("check\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := acquireLock(stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := run([]string{"check"}); err != nil {
		t.Fatalf("busy scheduled check: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check marker was not consumed: %v", err)
	}
}

func TestUpdateServiceSandboxAllowsManagedSetgidDirectories(t *testing.T) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	unit, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "systemd", "neproto-update.service"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unit), "RestrictSUIDSGID=true") {
		t.Fatal("update sandbox blocks the installer's required setgid chmod operations")
	}
}

func TestManagedUpdateDoesNotRepeatExistingSetgidChmod(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	installer, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(installer)
	for _, expected := range []string{
		`ensure_mode 2770 "$etc_neproto/geodata"`,
		`ensure_mode 2750 "$update_dir"`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("managed update repeats a restricted chmod instead of using %s", expected)
		}
	}
	for _, forbidden := range []string{
		`chmod 2770 "$etc_neproto/geodata"`,
		`chmod 2750 "$update_dir"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("managed update still performs unconditional restricted operation: %s", forbidden)
		}
	}
}

func TestWebStageDropsInheritedSetgidBeforeExtraction(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	installer, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}

	script := string(installer)
	stageCreated := strings.Index(script, `web_stage=$(mktemp -d "$opt_neproto/.web.XXXXXX")`)
	stageNormalized := strings.Index(script, `chmod 0700 "$web_stage"`)
	stageExtracted := strings.Index(script, `tar --no-same-owner --no-same-permissions -C "$web_stage" -xf -`)
	if stageCreated < 0 || stageNormalized < 0 || stageExtracted < 0 ||
		stageCreated >= stageNormalized || stageNormalized >= stageExtracted {
		t.Fatal("web staging must remove inherited SGID before payload extraction")
	}
}

func TestWebPayloadArchiveDropsInheritedSetgidWithoutMutatingSource(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	installer, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}

	script := string(installer)
	archiveNormalized := strings.Index(script, `tar -C "$script_dir/web" --mode='a-s,u+rwX,go+rX,go-w' -cf - .`)
	stageExtracted := strings.Index(script, `tar --no-same-owner --no-same-permissions -C "$web_stage" -xf -`)
	if archiveNormalized < 0 || stageExtracted < 0 || archiveNormalized >= stageExtracted {
		t.Fatal("web staging archive must remove inherited SGID before extraction")
	}
	if strings.Contains(script, `find "$script_dir/web" -type d -exec chmod`) ||
		strings.Contains(script, `cp -R --no-preserve=ownership,mode,timestamps -- "$script_dir/web/."`) {
		t.Fatal("legacy web staging must not mutate or copy source directory modes")
	}
}

func TestUpdaterWorkDirectoryDropsInheritedSetgid(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	engine, err := os.ReadFile(filepath.Join(repositoryRoot, "internal", "selfupdate", "engine.go"))
	if err != nil {
		t.Fatal(err)
	}

	source := string(engine)
	workCreated := strings.Index(source, `os.MkdirTemp(engine.store.Directory, ".work-*")`)
	workNormalized := strings.Index(source, `os.Chmod(workDirectory, 0o700)`)
	bundleExtracted := strings.Index(source, `ExtractBundle(archiveFile, workDirectory, release.Tag)`)
	if workCreated < 0 || workNormalized < 0 || bundleExtracted < 0 ||
		workCreated >= workNormalized || workNormalized >= bundleExtracted {
		t.Fatal("updater work directory must drop inherited SGID before extraction")
	}
}

func TestReleaseLifecycleRunsLegacyUpdaterSandbox(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	workflow, err := os.ReadFile(filepath.Join(repositoryRoot, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), `tests/legacy-updater-sandbox-smoke.sh`) {
		t.Fatal("release lifecycle does not exercise the legacy RestrictSUIDSGID updater sandbox")
	}
}

func TestInstallerPreservesExistingSetgidDirectoryOwnership(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	installer, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}

	script := string(installer)
	if !strings.Contains(script, `ensure_owner_group 0 "$service_gid" "$etc_neproto/geodata"`) {
		t.Fatal("installer must preserve an already-correct geodata directory owner and group")
	}
	if !strings.Contains(script, `ensure_owner_group 0 "$service_gid" "$update_dir"`) {
		t.Fatal("installer must preserve an already-correct updater directory owner and group")
	}
	if strings.Contains(script, `chown -R root:"$service_gid" "$etc_neproto/geodata"`) ||
		strings.Contains(script, `chown root:"$service_gid" "$update_dir"`) {
		t.Fatal("unconditional chown would clear inherited SGID under the legacy sandbox")
	}
}

func TestGeodataUpdaterDoesNotChmodExistingSetgidDirectory(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	updater, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "scripts", "update-geodata.sh"))
	if err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}

	script := string(updater)
	if !strings.Contains(script, `[[ -d $target ]] || install -d -m 0750 "$target"`) {
		t.Fatal("geodata updater must leave an existing setgid target directory unchanged")
	}
	if strings.Contains(script, "\ninstall -d -m 0750 \"$target\"\n") {
		t.Fatal("unconditional install -d would chmod the existing geodata directory")
	}
	if !strings.Contains(string(installer), `NEPROTO_GEODATA_PREPARE_ONLY=1 "$lib_dir/update-geodata" "$etc_neproto/geodata"`) {
		t.Fatal("isolated installer lifecycle must exercise production geodata directory preparation")
	}
	if !strings.Contains(string(installer), `NEPROTO_GEODATA_TEST_DOWNLOAD`) {
		t.Fatal("legacy updater smoke must be able to exercise verified geodata installation")
	}
}
