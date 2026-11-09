package httpserver

import (
	"errors"
	"testing"
)

func TestIsAddressInUse(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "linux", err: errors.New("listen tcp 0.0.0.0:8888: bind: address already in use"), want: true},
		{name: "windows", err: errors.New("listen tcp 0.0.0.0:8888: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted."), want: true},
		{name: "other", err: errors.New("listen tcp: permission denied"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAddressInUse(tt.err); got != tt.want {
				t.Fatalf("isAddressInUse(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
