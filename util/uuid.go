package util

import (
	uuid "github.com/google/uuid"
)

func GUID() string {
	return uuid.New().String()
}
