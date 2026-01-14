package service

import (
	"context"
	"errors"
	"fmt"
	"io"
)

type Service struct {
	User   *User
	TGBot  *TGBot
	Docker *Docker
}

func (service *Service) ScanContainers(ctx context.Context, w io.Writer) error {
	users, err := service.User.GetAll()
	if err != nil {
		return fmt.Errorf("unable to get users: %w", err)
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

		ok, err := service.Docker.IsContainerExists(ctx, user.Container)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if ok {
			continue
		}

		erased := true
		if _, err := service.User.UpdateContainer(user.ID, user.ContainerImage, ""); err != nil {
			errs = append(errs, err)
			erased = false
		}

		missing = append(missing, userContainer{user.ID, user.Container, erased})
	}

	if len(missing) == 0 {
		_, _ = fmt.Fprintln(w, "All containers exist.")
		return nil
	}

	_, _ = fmt.Fprintln(w, "There are disappeared containers:")
	_, _ = fmt.Fprint(w, "\n")

	for _, uc := range missing {
		_, _ = fmt.Fprintf(w, "- %d: %s. ", uc.user, uc.container)

		if uc.erased {
			_, _ = fmt.Fprintln(w, "Erased.")
		} else {
			_, _ = fmt.Fprintln(w, "Not erased!")
		}
	}

	return errors.Join(errs...)
}
