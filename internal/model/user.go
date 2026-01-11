package model

type User struct {
	ID             int64  `json:"-"`
	Username       string `json:",omitempty"`
	FirstName      string `json:",omitempty"`
	LastName       string `json:",omitempty"`
	ContainerImage string `json:",omitempty"`
	Container      string `json:",omitempty"`
	Admin          bool   `json:",omitempty"`
}
