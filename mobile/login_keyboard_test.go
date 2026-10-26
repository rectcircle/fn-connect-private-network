package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/test"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"testing"
)

func TestLoginKeyboardNavigationAndSubmit(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	v := newClientView(nil)
	defer v.cancel()
	v.window = a.NewWindow("test")
	v.window.SetContent(v.content)
	v.current.State = model.ClientUnconfigured
	v.showLogin(false)
	v.window.Resize(fyne.NewSize(420, 700))
	v.window.Show()
	v.loading.Hide()
	submissions := 0
	login := newTouchButton("test", func() { submissions++ })
	v.bindLoginKeyboard(login)
	enter := func(entry *loginEntry) { entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn}) }
	c := v.window.Canvas()
	c.Focus(v.fnID)
	enter(v.fnID)
	if c.Focused() != v.username {
		t.Fatal("ID return did not focus username")
	}
	enter(v.username)
	if c.Focused() != v.password {
		t.Fatal("username return did not focus password")
	}
	enter(v.password)
	enter(v.password)
	if submissions != 1 || c.Focused() != nil {
		t.Fatal("password must submit once and dismiss keyboard")
	}
	if v.password.Keyboard() != mobile.PasswordKeyboard {
		t.Fatal("password keyboard protection changed")
	}
	login.Disable()
	c.Focus(v.fnID)
	enter(v.fnID)
	if c.Focused() != v.fnID {
		t.Fatal("busy form advanced focus")
	}
	login.Enable()
	v.account.Hide()
	v.challenge.Show()
	c.Focus(v.accessCode)
	enter(v.accessCode)
	if c.Focused() != v.otp {
		t.Fatal("access-code return did not focus OTP")
	}
	enter(v.otp)
	if submissions != 2 {
		t.Fatal("OTP return did not submit")
	}
}
