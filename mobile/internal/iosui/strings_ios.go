//go:build ios

package iosui

/*
#include <stdlib.h>
*/
import "C"
import "fyne.io/fyne/v2/lang"
import "github.com/rectcircle/fn-connect-private-network/mobile/internal/uifeedback"

//export fncpn_ui_translate
func fncpn_ui_translate(text *C.char) *C.char {
	return C.CString(lang.L(C.GoString(text)))
}

//export fncpn_ui_feedback
func fncpn_ui_feedback(text *C.char) { uifeedback.Publish(C.GoString(text)) }
