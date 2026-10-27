//go:build linux

package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"sync"
	"sync/atomic"
	"time"
)

// Diagnostic-only state; this is not the product connection Manager.
var diagnostic = struct {
	sync.Mutex
	tun     int
	stop    chan struct{}
	done    chan struct{}
	sockets map[int]bool
	packets atomic.Int64
}{tun: -1, sockets: make(map[int]bool)}

func tunLoop(fd int, stop, done chan struct{}) {
	defer close(done)
	defer unix.Close(fd)
	packet := make([]byte, 1600)
	for {
		select {
		case <-stop:
			return
		default:
		}
		events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(events, 100)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		if events[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return
		}
		count, err := unix.Read(fd, packet)
		if err != nil {
			if err == unix.EAGAIN {
				continue
			}
			return
		}
		if reply := echoPacket(packet[:count]); reply != nil {
			if _, err = unix.Write(fd, reply); err == nil {
				diagnostic.packets.Add(1)
			}
		}
	}
}

func vpnAction(action, fd int) (map[string]any, error) {
	diagnostic.Lock()
	defer diagnostic.Unlock()
	switch action {
	case 1:
		if diagnostic.tun >= 0 {
			return nil, errors.New("TUN already running")
		}
		owned, err := unix.Dup(fd)
		if err != nil {
			return nil, err
		}
		unix.CloseOnExec(owned)
		diagnostic.tun = owned
		diagnostic.packets.Store(0)
		diagnostic.stop = make(chan struct{})
		diagnostic.done = make(chan struct{})
		go tunLoop(owned, diagnostic.stop, diagnostic.done)
		return map[string]any{"started": true}, nil
	case 2:
		if diagnostic.tun >= 0 {
			close(diagnostic.stop)
			done := diagnostic.done
			<-done
			diagnostic.tun = -1
		}
		for socket := range diagnostic.sockets {
			unix.Close(socket)
			delete(diagnostic.sockets, socket)
		}
		return map[string]any{"stopped": true, "packets": diagnostic.packets.Load()}, nil
	case 3:
		socket, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		diagnostic.sockets[socket] = true
		return map[string]any{"fd": socket}, nil
	case 4:
		if !diagnostic.sockets[fd] {
			return nil, errors.New("unknown diagnostic socket")
		}
		delete(diagnostic.sockets, fd)
		return map[string]any{"closed": true}, unix.Close(fd)
	case 5:
		if !diagnostic.sockets[fd] {
			return nil, errors.New("unknown diagnostic socket")
		}
		address := &unix.SockaddrInet4{Port: 53535, Addr: [4]byte{198, 18, 0, 1}}
		if err := unix.Sendto(fd, []byte("harmony-vpn-probe"), 0, address); err != nil {
			return map[string]any{"echo": false, "sendError": err.Error()}, nil
		}
		// Serialize diagnostic socket ownership while awaiting the bounded reply.
		events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, err := unix.Poll(events, int((1500*time.Millisecond)/time.Millisecond))
		if err != nil {
			return nil, err
		}
		reply := make([]byte, 128)
		n, _, err := unix.Recvfrom(fd, reply, 0)
		return map[string]any{"echo": err == nil && string(reply[:max(n, 0)]) == "harmony-vpn-probe"}, nil
	case 6:
		return map[string]any{"active": diagnostic.tun >= 0, "packets": diagnostic.packets.Load(), "sockets": len(diagnostic.sockets)}, nil
	}
	return nil, errors.New("unknown diagnostic action")
}

//export HarmonyVPN
func HarmonyVPN(action C.int, fd C.int) *C.char {
	result, err := vpnAction(int(action), int(fd))
	if err != nil {
		result = map[string]any{"error": err.Error()}
	}
	raw, _ := json.Marshal(result)
	return C.CString(string(raw))
}
