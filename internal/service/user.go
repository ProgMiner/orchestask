package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

import (
	"github.com/google/uuid"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/storage"
	"bypm.ru/orchestask/internal/util"
)

type User struct {
	storage *storage.User

	tgWaiters      map[model.ID]chan struct{}
	tgWaitersMutex sync.Mutex
}

var (
	ErrNoUser     = errors.New("user not found")
	ErrUserHaveTG = errors.New("user have already attached TG")
)

// TODO: restrict one-to-one between TG and SSH

func NewUser(storage *storage.Storage) (*User, error) {
	userStorage, err := storage.User()
	if err != nil {
		return nil, fmt.Errorf("unable to init user storage: %w", err)
	}

	service := &User{
		storage:   userStorage,
		tgWaiters: make(map[model.ID]chan struct{}),
	}

	return service, nil
}

func (service *User) Identify(username, pkey string) (*model.User, error) {
	return service.storage.FindByUsernameAndPKey(username, pkey)
}

func (service *User) Register(username, pkey string) (*model.User, error) {
	user, err := service.storage.FindByUsernameAndPKey(username, pkey)
	if err != nil {
		return nil, err
	}

	if user != nil {
		return user, fmt.Errorf("user is already exists")
	}

	return service.storage.Save(&model.User{SSHUsername: username, SSHPKey: pkey})
}

func (service *User) UpdateContainer(id model.ID, image, container string) (*model.User, error) {
	user, err := service.storage.FindByID(id)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, ErrNoUser
	}

	user.ContainerImage = image
	user.Container = container

	return service.storage.Save(user)
}

func (service *User) MakeTGLink(id model.ID) (*model.User, error) {
	user, err := service.storage.FindByID(id)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, ErrNoUser
	}

	if user.TGLink == "" {
		user.TGLink = uuid.NewString()
		return service.storage.Save(user)
	}

	return user, nil
}

func (service *User) WaitTGAttached(ctx context.Context, id model.ID) (*model.User, error) {
	ok, err := service.storage.ExistsByID(id)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, ErrNoUser
	}

	waiter, _ := util.Synchronized(&service.tgWaitersMutex, func() (<-chan struct{}, struct{}) {
		waiter, ok := service.tgWaiters[id]

		if !ok {
			waiter = make(chan struct{})
			service.tgWaiters[id] = waiter
		}

		return waiter, struct{}{}
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()

	case <-waiter:
		return service.storage.FindByID(id)
	}
}

func (service *User) AttachTG(
	tgLink string,
	tg int64,
	tgUsername, tgFirstName, tgLastName string,
) (*model.User, error) {
	user, err := service.storage.FindByTGLink(tgLink)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, ErrNoUser
	}

	if user.TG != 0 {
		return nil, ErrUserHaveTG
	}

	user.TG = tg
	user.TGUsername = tgUsername
	user.TGFirstName = tgFirstName
	user.TGLastName = tgLastName

	user, err = service.storage.Save(user)
	if err != nil {
		return nil, err
	}

	waiter, _ := util.Synchronized(&service.tgWaitersMutex, func() (chan struct{}, struct{}) {
		waiter, ok := service.tgWaiters[user.ID]

		if ok {
			delete(service.tgWaiters, user.ID)
		}

		return waiter, struct{}{}
	})

	if waiter != nil {
		close(waiter)
	}

	return user, nil
}
