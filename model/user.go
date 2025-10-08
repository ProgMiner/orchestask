package model

type User struct {
	ID             ID     `json:"-"`
	SSHUsername    string `json:",omitempty"`
	SSHPKey        string `json:",omitempty"`
	TG             int64  `json:",omitempty"`
	TGLink         string `json:",omitempty"`
	TGUsername     string `json:",omitempty"`
	TGFirstName    string `json:",omitempty"`
	TGLastName     string `json:",omitempty"`
	ContainerImage string `json:",omitempty"`
	Container      string `json:",omitempty"`
}
