package model

import (
	"regexp"
)

type ID string

var (
	idForbiddenRegex = regexp.MustCompile("[^a-zA-Z0-9_\\.+-]")
)

func MakeID(original string) ID {
	return ID(idForbiddenRegex.ReplaceAllString(original, "_"))
}
