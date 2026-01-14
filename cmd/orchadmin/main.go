package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/service"
	"bypm.ru/orchestask/internal/storage"
	"bypm.ru/orchestask/internal/util"
)

const (
	okExit = iota
	flagParseExit
	storageInitExit
	failExit
)

const (
	storageBaseDefault = "./data"
	storageBaseEnv     = "STORAGE_PATH"

	dockerImageDefault = ""
	dockerImageEnv     = "DOCKER_IMAGE"
)

var (
	storageBase = flag.String("storage", util.GetenvOr(storageBaseEnv, storageBaseDefault), "storage base path")
	dockerImage = flag.String("dockerImage", util.GetenvOr(dockerImageEnv, dockerImageDefault), "Task Docker image")
)

func parseArgs() error {
	cmdName := ""

	if len(os.Args) > 0 {
		cmdName = os.Args[0]
	}

	flag.Usage = func() {
		w := flag.CommandLine.Output()
		_, _ = fmt.Fprintf(w, "Usage: %s <command> [arguments...]\n\n", os.Args[0])
		_, _ = fmt.Fprintln(w, "Commands:")
		_, _ = fmt.Fprintln(w, "  stop-containers")
		_, _ = fmt.Fprintln(w, "\tStop all containers attached to the users")
		_, _ = fmt.Fprintln(w, "\nOptions:")
		flag.PrintDefaults()
	}

	// reinitialize CommandLine to change error handling
	flag.CommandLine.Init(cmdName, flag.ContinueOnError)
	return flag.CommandLine.Parse(os.Args[1:])
}

func makeService(ctx context.Context, storage *storage.Storage) (*service.Service, error) {
	docker, err := service.NewDocker(ctx, *dockerImage)
	if err != nil {
		return nil, err
	}

	user, err := service.NewUser(storage)
	if err != nil {
		return nil, err
	}

	service := &service.Service{
		User:   user,
		Docker: docker,
	}

	return service, nil
}

func commandScanContainers(ctx context.Context, service *service.Service) error {
	return service.ScanContainers(ctx, os.Stdout)
}

func commandStopContainers(ctx context.Context, service *service.Service) error {
	users, err := service.User.GetAll()
	if err != nil {
		return fmt.Errorf("unable to get users: %w", err)
	}

	var wg sync.WaitGroup

	type userError struct {
		u *model.User
		e error
	}

	var statusLine strings.Builder
	multiWriter := io.MultiWriter(&statusLine, os.Stdout)

	ueChan := make(chan userError)
	for _, user := range users {
		if user.Container == "" {
			_, _ = fmt.Fprint(multiWriter, ".")
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			err := service.Docker.StopContainer(ctx, user.Container)
			ueChan <- userError{user, err}
		}()
	}

	go func() {
		wg.Wait()
		close(ueChan)
	}()

	for ue := range ueChan {
		if ue.e == nil {
			_, _ = fmt.Fprint(multiWriter, "+")
		} else {
			fmt.Printf("\r%s", strings.Repeat(" ", statusLine.Len()))
			fmt.Printf("User %d: %v\n", ue.u.ID, ue.e)
			fmt.Printf("%s", statusLine.String())
			_, _ = fmt.Fprint(multiWriter, "-")
		}
	}

	fmt.Println("")
	return nil
}

func runCommand(ctx context.Context, service *service.Service) (int, string) {
	var err error

	switch flag.Arg(0) {
	case "scan-containers":
		err = commandScanContainers(ctx, service)

	case "stop-containers":
		err = commandStopContainers(ctx, service)

	default:
		flag.Usage()
		return flagParseExit, ""
	}

	if err != nil {
		return failExit, fmt.Sprintf("Error: %v", err)
	}

	return okExit, ""
}

func _main() (int, string) {
	if err := parseArgs(); err != nil {
		if err == flag.ErrHelp {
			return okExit, ""
		}

		return flagParseExit, ""
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseStorage, err := storage.Init(*storageBase)
	if err != nil {
		return storageInitExit, fmt.Sprintf("Unable to initialize storage: %v", err)
	}

	defer func() {
		if err := baseStorage.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Unable to close storage: %v\n", err)
		}
	}()

	storage, err := baseStorage.Storage()
	if err != nil {
		return storageInitExit, fmt.Sprintf("Unable to initialize storage: %v", err)
	}

	service, err := makeService(ctx, storage)
	if err != nil {
		return storageInitExit, fmt.Sprintf("Unable to initialize services: %v", err)
	}

	return runCommand(ctx, service)
}

func main() {
	code, err := _main()
	if err != "" {
		fmt.Fprintf(os.Stderr, "%s\n", err)
	}

	if code != okExit {
		os.Exit(code)
	}
}
