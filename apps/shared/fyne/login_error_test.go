package main

import (
	"errors"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"strings"
	"testing"
)

func TestLoginErrorMessageDistinguishesRemoteConnectionFromLocalAndAuthentication(t *testing.T) {
	for _, code := range []model.ErrorCode{model.ErrorUnavailable, model.ErrorTimeout} {
		err := model.WithOperation(model.NewError(code, "original", true), "native_session.connect")
		if !strings.Contains(loginErrorMessage(err), "FN Connect") {
			t.Fatal("missing connection guidance")
		}
	}
	for _, operation := range []string{"native_session.authenticate", "privileged.apply"} {
		err := model.WithOperation(model.NewError(model.ErrorUnavailable, "original", true), operation)
		if loginErrorMessage(err) != "original" {
			t.Fatal("unrelated error replaced")
		}
	}
	if loginErrorMessage(errors.New("plain")) != "plain" {
		t.Fatal("plain failure replaced")
	}
}
