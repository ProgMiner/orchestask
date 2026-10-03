package model

import "time"

type SSHUser struct {
	ID        ID        `json:"-"`
	PKey      string    `json:",omitempty"`
	TG        int64     `json:",omitempty"`
	CreatedAt time.Time `json:",omitempty"`
}
