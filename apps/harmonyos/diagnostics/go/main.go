package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"net/http"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

//export HarmonySmoke
func HarmonySmoke() C.int { return 42 }

//export HarmonyRun
func HarmonyRun(raw *C.char) *C.char {
	result := map[string]any{"version": runtime.Version(), "smoke": 42}
	var group sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() { defer group.Done(); <-time.After(time.Millisecond); mu.Lock(); total++; mu.Unlock() }()
	}
	group.Wait()
	runtime.GC()
	result["workers"] = total
	url := C.GoString(raw)
	if url != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if len(url) >= 6 && url[:6] == "wss://" {
			conn, _, err := websocket.Dial(ctx, url, nil)
			if err != nil {
				result["error"] = err.Error()
			} else {
				result["websocket"] = "connected"
				conn.CloseNow()
			}
		} else {
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err == nil && req.URL.Scheme != "https" {
				err = fmt.Errorf("HTTPS or WSS URL required")
			}
			if err == nil {
				response, e := http.DefaultClient.Do(req)
				err = e
				if e == nil {
					result["httpStatus"] = response.StatusCode
					response.Body.Close()
				}
			}
			if err != nil {
				result["error"] = err.Error()
			}
		}
	}
	bytes, _ := json.Marshal(result)
	return C.CString(string(bytes))
}

//export HarmonyFree
func HarmonyFree(value *C.char) { C.free(unsafe.Pointer(value)) }
