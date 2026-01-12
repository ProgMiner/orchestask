package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

import (
	tg "github.com/go-telegram/bot"
	tgModel "github.com/go-telegram/bot/models"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/storage"
	"bypm.ru/orchestask/internal/util"
)

type TGBot struct {
	storage       *storage.Storage
	userService   *User
	dockerService *Docker
	bot           *tg.Bot

	recentUsers      map[int64]struct{}
	recentUsersMutex sync.Mutex
}

func NewTGBot(
	apiKey string,
	storage *storage.Storage,
	userService *User,
	dockerService *Docker,
) (*TGBot, error) {
	bot, err := tg.New(apiKey, tg.WithMiddlewares(withNewGoroutine))

	if err != nil {
		return nil, fmt.Errorf("unable to initialize bot: %w", err)
	}

	service := &TGBot{
		storage:       storage,
		userService:   userService,
		dockerService: dockerService,
		bot:           bot,
		recentUsers:   make(map[int64]struct{}),
	}

	bot.RegisterHandler(
		tg.HandlerTypeMessageText,
		"start",
		tg.MatchTypeCommandStartOnly,
		service.makeHandler(service.handleStart),
	)

	bot.RegisterHandler(
		tg.HandlerTypeMessageText,
		"me",
		tg.MatchTypeCommand,
		service.makeHandler(service.handleMe),
	)

	bot.RegisterHandler(
		tg.HandlerTypeMessageText,
		"stopcontainer",
		tg.MatchTypeCommand,
		service.makeHandler(service.handleStopContainer),
	)

	bot.RegisterHandler(
		tg.HandlerTypeMessageText,
		"refreshdb",
		tg.MatchTypeCommand,
		service.makeHandler(service.handleRefreshDB),
	)

	bot.RegisterHandler(
		tg.HandlerTypeMessageText,
		"scancontainers",
		tg.MatchTypeCommand,
		service.makeHandler(service.handleScanContainers),
	)

	return service, nil
}

func (service *TGBot) Run(ctx context.Context) {
	ctx = util.WithLoggingScope(ctx, "Bot")
	go service.bot.Start(ctx)
}

func (service *TGBot) MakeStartLink(ctx context.Context, param string) (string, error) {
	me, err := service.bot.GetMe(ctx)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("https://t.me/%s?start=%s", me.Username, param), nil
}

func (service *TGBot) handleStart(ctx context.Context, update *tgModel.Update) error {
	text, _ := strings.CutPrefix(update.Message.Text, "/start")
	text = strings.TrimSpace(text)

	from := update.Message.From
	_, err := service.userService.AttachTG(text, from.ID, from.Username, from.FirstName, from.LastName)
	if err != nil {
		switch err {
		case ErrNoUser:
			_, err = service.sendText(ctx, update.Message.Chat.ID, "User not found")
		case ErrSSHUserHaveTG:
			_, err = service.sendText(ctx, update.Message.Chat.ID, `You was already attached\!`)
		}

		return err
	}

	_, err = service.sendText(ctx, update.Message.Chat.ID, `You have successfully attached\!`)
	return err
}

func (service *TGBot) handleMe(ctx context.Context, update *tgModel.Update) error {
	user, err := service.userService.GetByID(update.Message.From.ID)
	if err != nil && err != ErrNoUser {
		return err
	}

	if user == nil {
		_, err = service.sendText(ctx, update.Message.Chat.ID, "User not found")
		return err
	}

	lines := [][]string{}
	lines = append(lines, []string{`Hello\!`})

	{
		summaryLines := []string{`Summary:`}

		if user.Admin {
			summaryLines = append(summaryLines, `• You are an *admin*`)
		}

		if user.Container != "" {
			summaryLines = append(summaryLines, `• You have a Docker container`)
		} else {
			summaryLines = append(summaryLines, `• You have no a Docker container yet`)
		}

		lines = append(lines, summaryLines)
	}

	{
		cmdLines := []string{`Available commands:`}

		if user.Container != "" {
			cmdLines = append(cmdLines, `• /stopcontainer — Stop the Docker container`)
		}

		if user.Admin {
			cmdLines = append(cmdLines, `• /refreshdb — Refresh the database`)
			cmdLines = append(cmdLines, `• /scancontainers — Check the Docker containers existence`)
		}

		if len(cmdLines) > 1 {
			lines = append(lines, cmdLines)
		}
	}

	var msg strings.Builder

	for _, lines1 := range lines {
		if len(lines1) == 0 {
			continue
		}

		if msg.Len() > 0 {
			msg.WriteRune('\n')
		}

		for _, line := range lines1 {
			if len(line) == 0 {
				continue
			}

			msg.WriteString(line)
			msg.WriteRune('\n')
		}
	}

	_, err = service.sendText(ctx, update.Message.Chat.ID, msg.String())
	return err
}

func (service *TGBot) handleStopContainer(ctx context.Context, update *tgModel.Update) error {
	if ok, err := service.limitRate(ctx, update); !ok || err != nil {
		return err
	}

	user, err := service.userService.GetByID(update.Message.From.ID)
	if err != nil && err != ErrNoUser {
		return err
	}

	if user == nil {
		_, err = service.sendText(ctx, update.Message.Chat.ID, "User not found")
		return err
	}

	if user.Container == "" {
		_, err = service.sendText(ctx, update.Message.Chat.ID, "You have no a Docker container")
		return err
	}

	if err = service.dockerService.StopContainer(ctx, user.Container); err != nil {
		msg := `Unable to stop a container\. Contact an administrator`
		_, err1 := service.sendText(ctx, update.Message.Chat.ID, msg)
		return errors.Join(err, err1)
	}

	_, err = service.sendText(ctx, update.Message.Chat.ID, `Container stopped successfully\!`)
	return err
}

func (service *TGBot) handleRefreshDB(ctx context.Context, update *tgModel.Update) error {
	_, err := service.ensureAdmin(ctx, update)
	if err != nil {
		return err
	}

	err = service.storage.Reindex()
	if err != nil {
		msg := `Unable to refresh database\! See the details in the log\.`
		_, err1 := service.sendText(ctx, update.Message.Chat.ID, msg)

		return errors.Join(err, err1)
	}

	_, err = service.sendText(ctx, update.Message.Chat.ID, `Database have been refreshed successfully\!`)
	return err
}

func (service *TGBot) handleScanContainers(ctx context.Context, update *tgModel.Update) error {
	_, err := service.ensureAdmin(ctx, update)
	if err != nil {
		return err
	}

	users, err := service.userService.GetAll()
	if err != nil {
		msg := `Unable to get users\! See the details in the log\.`
		_, err1 := service.sendText(ctx, update.Message.Chat.ID, msg)

		return errors.Join(err, err1)
	}

	type userContainer struct {
		user      int64
		container string
		erased    bool
	}

	errs := []error{}
	missing := []userContainer{}
	for _, user := range users {
		if user.Container == "" {
			continue
		}

		ok, err := service.dockerService.IsContainerExists(ctx, user.Container)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if ok {
			continue
		}

		erased := true
		if _, err := service.userService.UpdateContainer(user.ID, user.ContainerImage, ""); err != nil {
			errs = append(errs, err)
			erased = false
		}

		missing = append(missing, userContainer{user.ID, user.Container, erased})
	}

	if len(missing) == 0 {
		_, err = service.sendText(ctx, update.Message.Chat.ID, `All containers exist\.`)
		return err
	}

	var msg strings.Builder
	msg.WriteString(`There are disappeared containers:`)
	msg.WriteRune('\n')

	for _, uc := range missing {
		status := `erased\.`

		if !uc.erased {
			status = `*not erased*\! Check the details in the log\.`
		}

		fmt.Fprintf(&msg, "• `%d` — `%s`, %s\n", uc.user, uc.container, status)
	}

	_, err = service.sendText(ctx, update.Message.Chat.ID, msg.String())
	errs = append(errs, err)

	return errors.Join(errs...)
}

func (service *TGBot) limitRate(ctx context.Context, update *tgModel.Update) (bool, error) {
	return util.Synchronized(&service.recentUsersMutex, func() (bool, error) {
		if _, ok := service.recentUsers[update.Message.From.ID]; ok {
			_, err := service.sendText(ctx, update.Message.Chat.ID, "Don't spam the commands, please")
			return false, err
		}

		service.recentUsers[update.Message.From.ID] = struct{}{}

		go func() {
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
			}

			util.Synchronized(&service.recentUsersMutex, func() (*struct{}, *struct{}) {
				delete(service.recentUsers, update.Message.From.ID)
				return nil, nil
			})
		}()

		return true, nil
	})
}

func (service *TGBot) ensureAdmin(ctx context.Context, update *tgModel.Update) (*model.User, error) {
	user, err := service.userService.GetByID(update.Message.From.ID)
	if err != nil {
		return nil, err
	}

	if !user.Admin {
		return nil, fmt.Errorf("User %d isn't an admin", user.ID)
	}

	return user, nil
}

func (service *TGBot) makeHandler(
	f func(ctx context.Context, update *tgModel.Update) error,
) tg.HandlerFunc {
	return func(ctx context.Context, _ *tg.Bot, update *tgModel.Update) {
		if err := f(ctx, update); err != nil {
			util.Log(ctx, "Unable to process update: %v", err)
		}
	}
}

func withNewGoroutine(handler tg.HandlerFunc) tg.HandlerFunc {
	return func(ctx context.Context, bot *tg.Bot, update *tgModel.Update) {
		go handler(ctx, bot, update)
	}
}

func (service *TGBot) sendText(ctx context.Context, charID any, text string) (*tgModel.Message, error) {
	return service.bot.SendMessage(ctx, &tg.SendMessageParams{
		ChatID:    charID,
		Text:      text,
		ParseMode: tgModel.ParseModeMarkdown,
	})
}
