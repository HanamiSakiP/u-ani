//go:build linux

package httpserver

import (
	"net"
	"testing"
)

func TestListenSocketInodes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	inodes, err := listenSocketInodes(port)
	if err != nil {
		t.Fatal(err)
	}
	if len(inodes) == 0 {
		t.Fatalf("no listening socket inode found for port %d", port)
	}
}
