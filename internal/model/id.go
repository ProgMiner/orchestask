package model

import (
	"fmt"
	"strconv"
	"strings"
)

type ID string

func StringToID(id string) ID {
	return ID(fmt.Sprintf("%s.json", id))
}

func IDToString(id ID) (string, error) {
	if res, ok := strings.CutSuffix(string(id), ".json"); ok {
		return res, nil
	}

	return "", fmt.Errorf("not an ID: %v", id)
}

func Int64ToID(id int64) ID {
	return ID(fmt.Sprintf("%d.json", id))
}

func IDToInt64(id ID) (int64, error) {
	strID, err := IDToString(id)
	if err != nil {
		return 0, err
	}

	res, err := strconv.ParseInt(strID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not an int64 ID: %w", err)
	}

	return res, nil
}
