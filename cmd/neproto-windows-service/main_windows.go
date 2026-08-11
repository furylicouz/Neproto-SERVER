//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"

	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/windowsclient"
)

const serviceName = "NeProtoService"

func main() {
	console := flag.Bool("console", false, "run in the current console")
	check := flag.Bool("check", false, "validate local service state")
	probe := flag.Bool("probe", false, "query the running service")
	cleanup := flag.Bool("cleanup", false, "remove a stale Windows route journal")
	version := flag.Bool("version", false, "print version")
	dataDirectory := flag.String("data-dir", defaultDataDirectory(), "service state directory")
	flag.Parse()
	if *version {
		fmt.Printf("NeProto Windows Service %s\n", buildinfo.Version)
		return
	}
	if *probe {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		response, err := windowsclient.CallPipe(ctx, windowsclient.Request{ID: "probe", Method: windowsclient.MethodStatus, Params: json.RawMessage(`{}`)})
		cancel()
		if err != nil || !response.OK {
			if err == nil {
				err = errors.New(response.Error)
			}
			log.Fatal(err)
		}
		raw, _ := json.Marshal(response.Result)
		fmt.Println(string(raw))
		return
	}
	if *cleanup {
		routes, err := windowsclient.NewWindowsRouteManager(*dataDirectory)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err = routes.Recover(ctx)
			cancel()
		}
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	configureFileLogging(*dataDirectory)
	if *check {
		if err := checkState(*dataDirectory); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		fmt.Println("NeProto Windows Service state is valid")
		return
	}
	if *console {
		if err := runConsole(*dataDirectory); err != nil {
			log.Fatal(err)
		}
		return
	}
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatal(err)
	}
	if !isService {
		log.Fatal("use --console for interactive execution")
	}
	if err := svc.Run(serviceName, serviceHandler{dataDirectory: *dataDirectory}); err != nil {
		log.Fatal(err)
	}
}

type runtime struct {
	controller *windowsclient.Controller
	pipe       *windowsclient.PipeServer
	cancel     context.CancelFunc
}

func newRuntime(parent context.Context, directory string) (*runtime, error) {
	routes, err := windowsclient.NewWindowsRouteManager(directory)
	if err != nil {
		return nil, err
	}
	recoveryContext, recoveryCancel := context.WithTimeout(parent, 15*time.Second)
	recoveryErr := routes.RecoverForStartup(recoveryContext)
	recoveryCancel()
	if recoveryErr != nil {
		log.Printf("defer Windows route recovery until connect: %v", recoveryErr)
	}
	store, err := windowsclient.OpenStore(directory, windowsclient.MachineProtector{})
	if err != nil {
		return nil, err
	}
	backend := windowsclient.NewWindowsBackendWithStartupRecovery(routes, recoveryErr)
	controller := windowsclient.NewController(store, backend)
	pipe, err := windowsclient.NewPipeServer(windowsclient.NewAPI(controller, store))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	result := &runtime{controller: controller, pipe: pipe, cancel: cancel}
	go func() {
		if serveErr := pipe.Serve(ctx); serveErr != nil && ctx.Err() == nil {
			log.Printf("IPC server stopped: %v", serveErr)
		}
	}()
	return result, nil
}

func (r *runtime) close() error {
	if r == nil {
		return nil
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	err := r.controller.Shutdown(shutdownContext)
	shutdownCancel()
	r.cancel()
	return err
}

type serviceHandler struct{ dataDirectory string }

func (h serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	runtime, err := newRuntime(context.Background(), h.dataDirectory)
	if err != nil {
		log.Printf("start service: %v", err)
		return true, 1
	}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			changes <- request.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			if err := runtime.close(); err != nil {
				log.Printf("stop service: %v", err)
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		default:
			log.Printf("unexpected service control request: %d", request.Cmd)
		}
	}
	_ = runtime.close()
	return false, 0
}

func runConsole(directory string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime, err := newRuntime(ctx, directory)
	if err != nil {
		return err
	}
	log.Printf("NeProto Windows Service %s listening on %s", buildinfo.Version, windowsclient.PipePath)
	<-ctx.Done()
	return runtime.close()
}

func checkState(directory string) error {
	store, err := windowsclient.OpenStore(directory, windowsclient.MachineProtector{})
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(struct {
		Version  string `json:"version"`
		Profiles int    `json:"profiles"`
		Selected string `json:"selected"`
	}{
		buildinfo.Version, len(store.Profiles()), store.SelectedProfileID(),
	})
	fmt.Println(string(raw))
	return nil
}

func defaultDataDirectory() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, "NeProto")
}

func configureFileLogging(directory string) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return
	}
	path := filepath.Join(directory, "service.log")
	if info, err := os.Stat(path); err == nil && info.Size() > 2<<20 {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		log.SetOutput(file)
		log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	}
}
