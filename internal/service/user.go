package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/storage"
	"bypm.ru/orchestask/internal/util"
)

type User struct {
	storage *storage.Storage

	waiters      map[model.ID]chan struct{}
	waitersMutex sync.Mutex
	approveMutex sync.Mutex
}

var (
	ErrNoUser          = errors.New("user not found")
	ErrAlreadyApproved = errors.New("user is already approved")
)

func NewUser(storage *storage.Storage) (*User, error) {
	service := &User{
		storage: storage,
		waiters: make(map[model.ID]chan struct{}),
	}

	return service, nil
}

func (service *User) Authenticate(pkey string) (*model.SSHUser, error) {
	return service.storage.SSHUser.FindByPKey(pkey)
}

func (service *User) Register(pkey string) (*model.SSHUser, error) {
	return service.storage.SSHUser.Save(&model.SSHUser{PKey: pkey})
}

func (service *User) GetByID(id int64) (*model.User, error) {
	user, err := service.storage.User.FindByID(id)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, ErrNoUser
	}

	return user, nil
}

func (service *User) GetAll() ([]*model.User, error) {
	return service.storage.User.FindAll()
}

func (service *User) UpdateContainer(id int64, image, container string) (*model.User, error) {
	user, err := service.GetByID(id)
	if err != nil {
		return nil, err
	}

	user.ContainerImage = image
	user.Container = container

	return service.storage.User.Save(user)
}

func (service *User) WaitApproved(ctx context.Context, id model.ID) (*model.SSHUser, error) {
	if ok, err := service.storage.SSHUser.ExistsByID(id); !ok || err != nil {
		if err == nil {
			err = ErrNoUser
		}

		return nil, err
	}

	waiter, _ := util.Synchronized(&service.waitersMutex, func() (<-chan struct{}, struct{}) {
		waiter, ok := service.waiters[id]

		if !ok {
			waiter = make(chan struct{})
			service.waiters[id] = waiter
		}

		return waiter, struct{}{}
	})

	// The approval could happen between the existence check above and the
	// waiter registration, so re-check to avoid waiting forever.
	sshUser, err := service.storage.SSHUser.FindByID(id)
	if err != nil {
		return nil, err
	}

	if sshUser != nil && sshUser.TG != 0 {
		return sshUser, nil
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()

	case <-waiter:
		return service.storage.SSHUser.FindByID(id)
	}
}

func (service *User) Approve(sshID model.ID) (*model.User, error) {
	service.approveMutex.Lock()
	defer service.approveMutex.Unlock()

	sshUser, err := service.storage.SSHUser.FindByID(sshID)
	if err != nil {
		return nil, err
	}

	if sshUser == nil {
		return nil, ErrNoUser
	}

	if sshUser.TG != 0 {
		return nil, ErrAlreadyApproved
	}

	var userID int64
	for range 100 {
		userID = rand.Int63()
		if userID == 0 {
			continue
		}

		exists, err := service.storage.User.ExistsByID(userID)
		if err != nil {
			return nil, err
		}

		if !exists {
			break
		}
	}

	if userID == 0 {
		return nil, errors.New("unable to generate a unique user ID")
	}

	if exists, err := service.storage.User.ExistsByID(userID); err != nil {
		return nil, err
	} else if exists {
		return nil, errors.New("unable to generate a unique user ID")
	}

	user, err := service.storage.User.Save(&model.User{ID: userID})
	if err != nil {
		return nil, fmt.Errorf("unable to create user: %w", err)
	}

	sshUser.TG = userID
	sshUser, err = service.storage.SSHUser.Save(sshUser)
	if err != nil {
		return nil, err
	}

	waiter, _ := util.Synchronized(&service.waitersMutex, func() (chan struct{}, *struct{}) {
		waiter, ok := service.waiters[sshUser.ID]

		if ok {
			delete(service.waiters, sshUser.ID)
		}

		return waiter, nil
	})

	if waiter != nil {
		close(waiter)
	}

	return user, nil
}

func (service *User) ListSSHUsers() ([]*model.SSHUser, error) {
	return service.storage.SSHUser.FindAll()
}
