package goladok3

import "github.com/google/uuid"

func newUUID() string { return uuid.New().String() }
