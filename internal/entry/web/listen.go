package web

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const listenPortAttempts = 20

var defaultListenAddr = "127.0.0.1:8080"

func openListener(addr string) (net.Listener, string, error) {
	addr = strings.TrimSpace(addr)
	if addr != "" {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, "", fmt.Errorf("监听 %s 失败：%w；该地址可能已被占用（常见是本机 llama-server），请改用 --listen 127.0.0.1:8090", addr, err)
		}
		return ln, listenerAddr(ln, addr), nil
	}
	host, portText, err := net.SplitHostPort(defaultListenAddr)
	if err != nil {
		return nil, "", err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, "", err
	}
	var last error
	for i := 0; i < listenPortAttempts; i++ {
		candidate := net.JoinHostPort(host, strconv.Itoa(port+i))
		ln, err := net.Listen("tcp", candidate)
		if err == nil {
			if i > 0 {
				fmt.Fprintf(os.Stderr, "warning: %s 已被占用，改用 http://%s\n", defaultListenAddr, listenerAddr(ln, candidate))
			}
			return ln, listenerAddr(ln, candidate), nil
		}
		last = err
	}
	return nil, "", fmt.Errorf("默认端口 %s 起连续 %d 个地址均被占用：%w；请用 --listen 指定空闲地址", defaultListenAddr, listenPortAttempts, last)
}

func listenerAddr(ln net.Listener, fallback string) string {
	if ln == nil || ln.Addr() == nil {
		return fallback
	}
	return ln.Addr().String()
}
