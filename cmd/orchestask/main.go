package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

import (
	"bypm.ru/orchestask/internal/service"
	"bypm.ru/orchestask/internal/ssh"
	"bypm.ru/orchestask/internal/storage"
	"bypm.ru/orchestask/internal/util"
)

const (
	okExit = iota
	flagParseExit
	storageInitExit
	sshInitExit
	sshServerExit
)

const (
	storageBaseDefault = "./data"
	storageBaseEnv     = "STORAGE_PATH"

	// tgHostDefault = ""
	// tgHostEnv     = "TG_HOST"

	tgApiDefault = ""
	tgApiEnv     = "TG_API"

	sshHostDefault = ":22"
	sshHostEnv     = "SSH_HOST"

	sshKeyFileDefault = "./id_rsa"
	sshKeyFileEnv     = "SSH_KEY_FILE"

	dockerImageDefault = ""
	dockerImageEnv     = "DOCKER_IMAGE"

	dockerImageSSHUserDefault = "student"
	dockerImageSSHUserEnv     = "DOCKER_IMAGE_SSH_USERNAME"
)

var (
	storageBase = flag.String("storage", util.GetenvOr(storageBaseEnv, storageBaseDefault), "storage base path")
	// TODO: support TG API webhook
	// tgHost = flag.String("tgHost", util.GetenvOr(tgHostEnv, tgHostDefault), "Telegram webhook server address")
	tgApi              = flag.String("tgApi", util.GetenvOr(tgApiEnv, tgApiDefault), "Telegram bot API key")
	sshHost            = flag.String("sshHost", util.GetenvOr(sshHostEnv, sshHostDefault), "SSH server address")
	sshKeyFile         = flag.String("sshKey", util.GetenvOr(sshKeyFileEnv, sshKeyFileDefault), "SSH key file")
	dockerImage        = flag.String("dockerImage", util.GetenvOr(dockerImageEnv, dockerImageDefault), "Task Docker image")
	dockerImageSSHUser = flag.String("dockerImageSSHUser", util.GetenvOr(dockerImageSSHUserEnv, dockerImageSSHUserDefault), "Task Docker image SSH username")
)

func parseArgs() error {
	cmdName := ""

	if len(os.Args) > 0 {
		cmdName = os.Args[0]
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

	tgBot, err := service.NewTGBot(*tgApi, user)
	if err != nil {
		return nil, err
	}

	service := &service.Service{
		User:   user,
		TGBot:  tgBot,
		Docker: docker,
	}

	return service, nil
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

func _main() (int, string) {
	if err := parseArgs(); err != nil {
		if err == flag.ErrHelp {
			return okExit, ""
		}

		return flagParseExit, ""
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	storage, err := storage.Init(*storageBase)
	if err != nil {
		return storageInitExit, fmt.Sprintf("Unable to initialize storage: %v", err)
	}

	defer func() {
		if err := storage.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Unable to close storage: %v\n", err)
		}
	}()

	service, err := makeService(ctx, storage)
	if err != nil {
		return storageInitExit, fmt.Sprintf("Unable to initialize services: %v", err)
	}

	sshServer, err := ssh.MakeSSHServer(service, *sshHost, *sshKeyFile, *dockerImageSSHUser)
	if err != nil {
		return sshInitExit, fmt.Sprintf("Unable to initialize SSH server: %v", err)
	}

	service.TGBot.Run(ctx)

	if err := sshServer.Run(ctx); err != nil {
		return sshServerExit, fmt.Sprintf("SSH server error: %v", err)
	}

	return okExit, ""
}
