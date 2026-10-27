package main

import "github.com/rectcircle/fn-connect-private-network/internal/model"

func loginErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	failure := model.AsError(err)
	if failure.Operation == "native_session.connect" && (failure.Code == model.ErrorUnavailable || failure.Code == model.ErrorTimeout) {
		return tr("服务不可用。请检查网络连接、NAS 上的 FN Connect 是否已启用，以及 FN Connect ID 是否正确。")
	}
	return err.Error()
}
