package service

import (
	"context"
	"fmt"
	"os"
	"strings"
)

import (
	tg "github.com/go-telegram/bot"
	tgModel "github.com/go-telegram/bot/models"
)

type TGBot struct {
	userService *User
	bot         *tg.Bot
}

func NewTGBot(apiKey string, userService *User) (*TGBot, error) {
	bot, err := tg.New(apiKey)

	if err != nil {
		return nil, fmt.Errorf("unable to initialize bot: %w", err)
	}

	service := &TGBot{userService, bot}

	bot.RegisterHandler(
		tg.HandlerTypeMessageText,
		"start",
		tg.MatchTypeCommandStartOnly,
		service.makeHandler(service.handleStart),
	)

	return service, nil
}

func (service *TGBot) Run(ctx context.Context) {
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
	user, err := service.userService.AttachTG(text, from.ID, from.Username, from.FirstName, from.LastName)
	if err != nil {
		switch err {
		case ErrNoUser:
			_, err = service.sendText(ctx, update.Message.Chat.ID, "User not found")
		case ErrUserHaveTG:
			_, err = service.sendText(ctx, update.Message.Chat.ID, "You was already attached\\!")
		}

		return err
	}

	fmt.Printf("[%s] Attached to TG %v (%v)\n", user.ID, update.Message.From.ID, update.Message.From.Username)
	_, err = service.sendText(ctx, update.Message.Chat.ID, "You have successfully attached\\!")
	return err
}

func (service *TGBot) makeHandler(
	f func(ctx context.Context, update *tgModel.Update) error,
) tg.HandlerFunc {
	return func(ctx context.Context, _ *tg.Bot, update *tgModel.Update) {
		if err := f(ctx, update); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "[TG] Unable to process update: %v\n", err)
		}
	}
}

func (service *TGBot) sendText(ctx context.Context, charID any, text string) (*tgModel.Message, error) {
	return service.bot.SendMessage(ctx, &tg.SendMessageParams{
		ChatID:    charID,
		Text:      text,
		ParseMode: tgModel.ParseModeMarkdown,
	})
}
