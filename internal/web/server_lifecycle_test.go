package web

import (
	"testing"
	"time"
)

func TestNewHTTPServerHasBoundedTimeouts(t *testing.T) {
	s := &Server{}
	srv := s.newHTTPServer("127.0.0.1:8720")
	if srv.Addr != "127.0.0.1:8720" {
		t.Errorf("Addr = %q, want 127.0.0.1:8720", srv.Addr)
	}
	if srv.Handler == nil {
		t.Errorf("Handler is nil, want s.Handler()")
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 120*time.Second {
		t.Errorf("WriteTimeout = %v, want 120s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", srv.IdleTimeout)
	}
}

func TestListenAndServeRefusesNonLoopbackWithoutToken(t *testing.T) {
	s := &Server{Host: "0.0.0.0", CloudToken: ""}
	err := s.ListenAndServe()
	if err == nil {
		t.Fatalf("expected error binding %q without admin token, got nil", "0.0.0.0")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"localhost", true},
		{"", true},
		{"0.0.0.0", false},
		{"192.168.1.10", false},
	}
	for _, tc := range cases {
		if got := isLoopbackHost(tc.host); got != tc.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
