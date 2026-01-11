package model

type SSHUser struct {
	ID   ID     `json:"-"`
	PKey string `json:",omitempty"`
	TG   int64  `json:",omitempty"`
}
