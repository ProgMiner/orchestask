package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/service"
	"bypm.ru/orchestask/internal/storage"
	"bypm.ru/orchestask/internal/util"
	"golang.org/x/sync/errgroup"
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
		_, _ = fmt.Fprintln(w, "  scan-containers")
		_, _ = fmt.Fprintln(w, "\tCheck the Docker containers existence")
		_, _ = fmt.Fprintln(w, "  stop-containers")
		_, _ = fmt.Fprintln(w, "\tStop all containers attached to the users")
		_, _ = fmt.Fprintln(w, "  csv [filename]")
		_, _ = fmt.Fprintln(w, "\tMake a summary CSV file")
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

	wg, wgCtx := errgroup.WithContext(ctx)

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

		wg.Go(func() error {
			err := service.Docker.StopContainer(wgCtx, user.Container)
			ueChan <- userError{user, err}
			return nil
		})
	}

	go func() {
		_ = wg.Wait()
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

// TODO: somehow make a multi-page ODS instead of this piece of shit
func commandCSV(ctx context.Context, storage *storage.Storage, service *service.Service) error {
	path := flag.Arg(1)

	if path == "" {
		path = "./summary.csv"
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("unable to create file: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	users, err := service.User.GetAll()
	if err != nil {
		return fmt.Errorf("unable to get users: %w", err)
	}

	sshUsers, err := storage.SSHUser.FindAll()
	if err != nil {
		return fmt.Errorf("unable to get SSH users: %w", err)
	}

	sshUsersSummary := make(map[int64]int, len(users))
	for _, sshUser := range sshUsers {
		if sshUser.TG == 0 {
			fmt.Printf("SSH user with public key %s haven't attached a TG account\n", sshUser.PKey)
			continue
		}

		sshUsersSummary[sshUser.TG]++
	}

	csvWriter := csv.NewWriter(file)
	csvWriterErrs := []error{}

	writeCSV := func(values ...string) {
		csvWriterErrs = append(csvWriterErrs, csvWriter.Write(values))

		csvWriter.Flush()
		csvWriterErrs = append(csvWriterErrs, csvWriter.Error())
	}

	writeCSV(
		"ID",
		"Username",
		"First name",
		"Last name",
		"SSH users",
		"Container image",
		"Container",
		"Container logs",
	)

	for _, user := range users {
		containerLogs := ""

		if user.Container != "" {
			res, err := service.Docker.GetContainerLogs(ctx, user.Container)
			if err != nil {
				return fmt.Errorf("unable to get container logs of user %d: %w", user.ID, err)
			}

			containerLogs = res
		}

		writeCSV(
			fmt.Sprintf("%d", user.ID),
			user.Username,
			user.FirstName,
			user.LastName,
			fmt.Sprintf("%d", sshUsersSummary[user.ID]),
			user.ContainerImage,
			user.Container,
			containerLogs,
		)
	}

	return errors.Join(csvWriterErrs...)
}

func runCommand(ctx context.Context, storage *storage.Storage, service *service.Service) (int, string) {
	var err error

	switch flag.Arg(0) {
	case "scan-containers":
		err = commandScanContainers(ctx, service)

	case "stop-containers":
		err = commandStopContainers(ctx, service)

	case "csv":
		err = commandCSV(ctx, storage, service)

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

	return runCommand(ctx, storage, service)
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
