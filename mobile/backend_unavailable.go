package main

import (
	"context"
	"errors"
)

type unavailableClient struct{ reason string }

func (c unavailableClient) Call(context.Context, string, any, any) error {
	return errors.New(c.reason)
}
