//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/windowscandidate"
)

const (
	serviceName = "NeProtoService"
	footerSize  = 24
)

var payloadMagic = [16]byte{'N', 'P', '2', 'W', 'I', 'N', 'S', 'E', 'T', 'U', 'P', 'V', '1', 0, 0, 0}
var setupMode = "legacy"

type setupLayout struct {
	application string
	service     string
	wintun      string
	uninstaller string
	unified     bool
}

func setupLayoutForMode(mode string) (setupLayout, error) {
	switch mode {
	case "legacy":
		return setupLayout{application: "NeProto.exe", service: "NeProto.Service.exe", wintun: "wintun.dll", uninstaller: "NeProto.Uninstall.exe"}, nil
	case "unified":
		return setupLayout{application: `app\neproto_client.exe`, service: `service\NeProto.Service.exe`, wintun: `service\wintun.dll`, uninstaller: "NeProto.Uninstall.exe", unified: true}, nil
	default:
		return setupLayout{}, errors.New("unsupported Windows setup mode")
	}
}

func safePayloadPath(root, name string) (string, error) {
	if name == "" || len(name) > 240 || strings.ContainsAny(name, `\:`) {
		return "", errors.New("установочный payload содержит некорректный путь")
	}
	clean := path.Clean(name)
	if clean == "." || path.IsAbs(clean) || !fs.ValidPath(clean) {
		return "", errors.New("установочный payload содержит небезопасный путь")
	}
	destination := filepath.Join(root, filepath.FromSlash(clean))
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, `..`+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("установочный payload выходит за каталог установки")
	}
	return destination, nil
}

func verifyPayloadForMode(directory, mode, expectedVersion string) error {
	layout, err := setupLayoutForMode(mode)
	if err != nil {
		return err
	}
	required := []string{layout.application, layout.service, layout.wintun, layout.uninstaller}
	if layout.unified {
		manifest, err := windowscandidate.LoadAndVerify(directory)
		if err != nil {
			return fmt.Errorf("проверка manifest установщика: %w", err)
		}
		if manifest.Version != expectedVersion {
			return errors.New("версия payload не совпадает с версией установщика")
		}
		required = append(required, `app\flutter_windows.dll`, `app\data\icudtl.dat`, `app\data\app.so`, `app\data\flutter_assets\AssetManifest.bin`)
	} else {
		required = append(required, "WINTUN-LICENSE.txt")
	}
	for _, name := range required {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("в payload отсутствует %s", name)
		}
	}
	return nil
}

func main() {
	uninstall := hasArgument("/uninstall") || hasArgument("--uninstall")
	purge := hasArgument("/purge") || hasArgument("--purge")
	if hasArgument("/verify") || hasArgument("--verify") {
		directory, err := os.MkdirTemp("", "neproto-setup-verify-")
		if err == nil {
			defer os.RemoveAll(directory)
			err = extractPayload(directory)
		}
		if err == nil {
			err = verifyPayload(directory)
		}
		if err != nil {
			showError(err)
			os.Exit(1)
		}
		return
	}
	if !isElevated() {
		if err := elevate(); err != nil {
			showError(err)
		}
		return
	}
	var err error
	if uninstall {
		err = uninstallApplication(purge)
	} else {
		err = installApplication()
	}
	if err != nil {
		showError(err)
		os.Exit(1)
	}
	if uninstall {
		showMessage("NeProto удалён.")
	} else {
		showMessage("NeProto установлен. Откройте приложение из меню Пуск.")
	}
}

func installApplication() error {
	layout, err := setupLayoutForMode(setupMode)
	if err != nil {
		return err
	}
	programFiles := os.Getenv("ProgramFiles")
	programData := os.Getenv("ProgramData")
	if programFiles == "" || programData == "" {
		return errors.New("не найдены системные каталоги Windows")
	}
	target := filepath.Join(programFiles, "NeProto")
	data := filepath.Join(programData, "NeProto")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	_ = configureDataACL(data)

	staging := target + ".new"
	backup := target + ".previous"
	_ = os.RemoveAll(staging)
	_ = os.RemoveAll(backup)
	if err := extractPayload(staging); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := verifyPayload(staging); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	previousService := installedServiceRelative(target)
	if err := stopAndDeleteService(); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	cleanupInstalledRuntime(target, data)
	if pathExists(target) {
		if err := os.Rename(target, backup); err != nil {
			restorePreviousService(target, data, previousService)
			return fmt.Errorf("не удалось подготовить обновление: %w", err)
		}
	}
	if err := os.Rename(staging, target); err != nil {
		_ = os.Rename(backup, target)
		restorePreviousService(target, data, previousService)
		return fmt.Errorf("не удалось установить файлы: %w", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = stopAndDeleteService()
			_ = os.RemoveAll(target)
			_ = os.Rename(backup, target)
			restorePreviousService(target, data, previousService)
		}
	}()
	if err := registerService(target, data, layout.service); err != nil {
		return err
	}
	if err := createShortcut(target, layout.application); err != nil {
		return err
	}
	if err := writeUninstallRegistry(target, layout); err != nil {
		return err
	}
	rollback = false
	_ = os.RemoveAll(backup)
	return nil
}

func uninstallApplication(purge bool) error {
	programFiles := os.Getenv("ProgramFiles")
	programData := os.Getenv("ProgramData")
	target := filepath.Join(programFiles, "NeProto")
	data := filepath.Join(programData, "NeProto")
	if err := stopAndDeleteService(); err != nil {
		return err
	}
	cleanupInstalledRuntime(target, data)
	_ = os.Remove(filepath.Join(programData, `Microsoft\Windows\Start Menu\Programs\NeProto.lnk`))
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\NeProto`)
	self, _ := os.Executable()
	if purge {
		_ = os.RemoveAll(data)
	}
	if strings.EqualFold(filepath.Dir(self), target) {
		entries, _ := os.ReadDir(target)
		for _, entry := range entries {
			entryPath := filepath.Join(target, entry.Name())
			if !strings.EqualFold(entryPath, self) {
				_ = os.RemoveAll(entryPath)
			}
		}
		selfPath, _ := windows.UTF16PtrFromString(self)
		targetPath, _ := windows.UTF16PtrFromString(target)
		_ = windows.MoveFileEx(selfPath, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
		_ = windows.MoveFileEx(targetPath, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
	} else {
		_ = os.RemoveAll(target)
	}
	return nil
}

func cleanupInstalledRuntime(target, data string) {
	for _, relative := range []string{"NeProto.Service.exe", `service\NeProto.Service.exe`} {
		servicePath := filepath.Join(target, relative)
		if fileExists(servicePath) {
			_ = exec.Command(servicePath, "--cleanup", "--data-dir", data).Run()
		}
	}
}

func installedServiceRelative(target string) string {
	for _, relative := range []string{`service\NeProto.Service.exe`, "NeProto.Service.exe"} {
		if fileExists(filepath.Join(target, relative)) {
			return relative
		}
	}
	return ""
}

func restorePreviousService(target, data, serviceRelative string) {
	if serviceRelative != "" && fileExists(filepath.Join(target, serviceRelative)) {
		_ = registerService(target, data, serviceRelative)
	}
}

func extractPayload(destination string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	file, err := os.Open(executable)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < footerSize {
		return errors.New("установочный пакет повреждён")
	}
	footer := make([]byte, footerSize)
	if _, err := file.ReadAt(footer, info.Size()-footerSize); err != nil {
		return err
	}
	if !bytes.Equal(footer[8:], payloadMagic[:]) {
		return errors.New("в установщике отсутствует payload")
	}
	size := int64(binary.LittleEndian.Uint64(footer[:8]))
	if size <= 0 || size > 512<<20 || size > info.Size()-footerSize {
		return errors.New("некорректный размер payload")
	}
	reader, err := zip.NewReader(io.NewSectionReader(file, info.Size()-footerSize-size, size), size)
	if err != nil {
		return errors.New("установочный payload повреждён")
	}
	return extractArchive(reader, destination, setupMode)
}

func extractArchive(reader *zip.Reader, destination, mode string) error {
	if _, err := setupLayoutForMode(mode); err != nil {
		return err
	}
	if len(reader.File) == 0 || len(reader.File) > 4096 {
		return errors.New("установочный payload содержит некорректное число файлов")
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	legacyAllowed := map[string]bool{"NeProto.exe": true, "NeProto.Service.exe": true, "wintun.dll": true, "WINTUN-LICENSE.txt": true, "NeProto.Uninstall.exe": true}
	seen := make(map[string]struct{}, len(reader.File))
	var total uint64
	for _, entry := range reader.File {
		outputPath, err := safePayloadPath(destination, entry.Name)
		if err != nil {
			return err
		}
		cleanName := path.Clean(entry.Name)
		folded := strings.ToLower(cleanName)
		if _, exists := seen[folded]; exists {
			return errors.New("установочный payload содержит повторяющийся путь")
		}
		seen[folded] = struct{}{}
		info := entry.FileInfo()
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return errors.New("установочный payload содержит неожиданный файл")
		}
		if mode == "legacy" && (!legacyAllowed[cleanName] || info.IsDir()) {
			return errors.New("установочный payload содержит неожиданный файл")
		}
		if info.IsDir() {
			if err := os.MkdirAll(outputPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > 512<<20 || total > (1<<30)-entry.UncompressedSize64 {
			return errors.New("установочный payload превышает допустимый размер")
		}
		total += entry.UncompressedSize64
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err == nil {
			var written int64
			written, err = io.Copy(output, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
			if err == nil && uint64(written) != entry.UncompressedSize64 {
				err = errors.New("размер файла в payload не совпадает с архивом")
			}
			if closeErr := output.Close(); err == nil {
				err = closeErr
			}
		}
		_ = input.Close()
		if err != nil {
			_ = os.Remove(outputPath)
			return err
		}
	}
	return nil
}

func verifyPayload(directory string) error {
	return verifyPayloadForMode(directory, setupMode, buildinfo.Version)
}

func registerService(target, data, serviceRelative string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.CreateService(serviceName, filepath.Join(target, serviceRelative), mgr.Config{
		DisplayName: "NeProto NP/2 VPN", Description: "NeProto NP/2 system tunnel service",
		StartType: mgr.StartAutomatic, DelayedAutoStart: true, ErrorControl: mgr.ErrorNormal,
		Dependencies: []string{"Tcpip", "Dnscache"}, SidType: windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}, "--data-dir", data)
	if err != nil {
		return err
	}
	defer service.Close()
	if err := service.Start(); err != nil {
		_ = service.Delete()
		return err
	}
	if err := waitForRunningService(service.Query, 80, time.Sleep); err != nil {
		_, _ = service.Control(svc.Stop)
		_ = service.Delete()
		return fmt.Errorf("служба NeProto не запустилась: %w", err)
	}
	return nil
}

func waitForRunningService(query func() (svc.Status, error), attempts int, pause func(time.Duration)) error {
	if attempts <= 0 || query == nil || pause == nil {
		return errors.New("invalid service startup check")
	}
	for attempt := 0; attempt < attempts; attempt++ {
		status, err := query()
		if err != nil {
			return err
		}
		switch status.State {
		case svc.Running:
			return nil
		case svc.Stopped:
			if status.ServiceSpecificExitCode != 0 {
				return fmt.Errorf("service-specific code %d", status.ServiceSpecificExitCode)
			}
			return fmt.Errorf("Windows code %d", status.Win32ExitCode)
		}
		pause(250 * time.Millisecond)
	}
	return errors.New("service startup timed out")
}

func stopAndDeleteService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(serviceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return err
	}
	defer service.Close()
	if status, queryErr := service.Query(); queryErr == nil && status.State != svc.Stopped {
		_, _ = service.Control(svc.Stop)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			status, queryErr = service.Query()
			if queryErr == nil && status.State == svc.Stopped {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	return service.Delete()
}

func createShortcut(target, applicationRelative string) error {
	shortcut := filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\NeProto.lnk`)
	application := filepath.Join(target, applicationRelative)
	script := "$s=(New-Object -ComObject WScript.Shell).CreateShortcut('" + psQuote(shortcut) + "');$s.TargetPath='" + psQuote(application) + "';$s.WorkingDirectory='" + psQuote(filepath.Dir(application)) + "';$s.Description='NeProto NP/2 VPN';$s.Save()"
	return runPowerShell(script)
}

func writeUninstallRegistry(target string, layout setupLayout) error {
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\NeProto`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	values := map[string]string{"DisplayName": "NeProto", "DisplayVersion": strings.TrimPrefix(buildinfo.Version, "np2-"), "Publisher": "NeProto", "InstallLocation": target, "UninstallString": `"` + filepath.Join(target, layout.uninstaller) + `" /uninstall`, "DisplayIcon": filepath.Join(target, layout.application)}
	for name, value := range values {
		if err := key.SetStringValue(name, value); err != nil {
			return err
		}
	}
	return key.SetDWordValue("NoModify", 1)
}

func configureDataACL(path string) error {
	return exec.Command("icacls.exe", path, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F").Run()
}

func elevate() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(self)
	arguments := append([]string{"--elevated"}, os.Args[1:]...)
	params, _ := windows.UTF16PtrFromString(joinWindowsArguments(arguments))
	working, _ := windows.UTF16PtrFromString(filepath.Dir(self))
	return windows.ShellExecute(0, verb, file, params, working, windows.SW_SHOWNORMAL)
}

func isElevated() bool {
	token := windows.GetCurrentProcessToken()
	return token.IsElevated()
}

func hasArgument(value string) bool {
	for _, argument := range os.Args[1:] {
		if strings.EqualFold(argument, value) {
			return true
		}
	}
	return false
}
func fileExists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }
func pathExists(path string) bool { _, err := os.Stat(path); return err == nil }
func psQuote(value string) string { return strings.ReplaceAll(value, "'", "''") }

func joinWindowsArguments(arguments []string) string {
	quoted := make([]string, len(arguments))
	for index, value := range arguments {
		quoted[index] = `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return strings.Join(quoted, " ")
}

func runPowerShell(script string) error {
	encoded := utf16.Encode([]rune(script))
	raw := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		binary.LittleEndian.PutUint16(raw[index*2:], value)
	}
	return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw)).Run()
}

func showMessage(message string) {
	text, _ := windows.UTF16PtrFromString(message)
	caption, _ := windows.UTF16PtrFromString("NeProto")
	_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONINFORMATION)
}
func showError(err error) {
	text, _ := windows.UTF16PtrFromString(err.Error())
	caption, _ := windows.UTF16PtrFromString("Ошибка установки NeProto")
	_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
}
