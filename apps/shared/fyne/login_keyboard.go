package main

func (v *clientView) bindLoginKeyboard(login *touchButton) {
	if platform, ok := v.backend.(interface{ SetKeyboardAction(int) }); ok {
		for _, entry := range []*loginEntry{v.fnID, v.username, v.password, v.accessCode, v.otp} {
			entry.onKeyboard = platform.SetKeyboardAction
		}
	}
	v.password.submitKey = true
	v.otp.submitKey = true
	ready := func(entry *loginEntry) bool {
		return v.ctx.Err() == nil && v.editing && !login.Disabled() && v.window != nil && v.window.Canvas().Focused() == entry
	}
	next := func(from, to *loginEntry, challenge bool) func(string) {
		return func(string) {
			if !ready(from) || challenge != v.challenge.Visible() {
				return
			}
			v.window.Canvas().Focus(to)
		}
	}
	submit := func(entry *loginEntry, challenge bool) func(string) {
		return func(string) {
			if !ready(entry) || challenge != v.challenge.Visible() {
				return
			}
			v.window.Canvas().Unfocus()
			login.OnTapped() // Exactly the same validation and authorization path as the button.
		}
	}
	v.fnID.OnSubmitted = next(v.fnID, v.username, false)
	v.username.OnSubmitted = next(v.username, v.password, false)
	v.password.OnSubmitted = submit(v.password, false)
	v.accessCode.OnSubmitted = next(v.accessCode, v.otp, true)
	v.otp.OnSubmitted = submit(v.otp, true)
}
