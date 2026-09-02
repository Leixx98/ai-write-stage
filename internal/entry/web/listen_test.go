package web

import (
	"net"
	"strings"
	"testing"
)

func TestOpenListenerUsesExplicitAddress(t *testing.T) {
	ln, addr, err := openListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("openListener: %v", err)
	}
	defer ln.Close()
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" || port == "0" || port == "" {
		t.Fatalf("bound address = %q", addr)
	}
}

func TestOpenListenerRejectsBusyExplicitAddress(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	_, _, err = openListener(busy.Addr().String())
	if err == nil {
		t.Fatal("expected explicit busy address to fail")
	}
	if !strings.Contains(err.Error(), "--listen") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenListenerFallsBackWhenDefaultBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	previous := defaultListenAddr
	defaultListenAddr = busy.Addr().String()
	defer func() { defaultListenAddr = previous }()
	ln, addr, err := openListener("")
	if err != nil {
		t.Fatalf("openListener: %v", err)
	}
	defer ln.Close()
	if addr == busy.Addr().String() {
		t.Fatalf("did not fall back from %s", addr)
	}
}
