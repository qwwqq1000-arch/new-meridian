package main

import (
	"fmt"
	"net/http"
	"os"
)

func newServer() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return mux
}

func main() {
	addr := os.Getenv("NATIVE_EGRESS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	fmt.Fprintf(os.Stderr, "native-egress listening on %s\n", addr)
	if err := http.ListenAndServe(addr, newServer()); err != nil {
		fmt.Fprintln(os.Stderr, "native-egress exited:", err)
		os.Exit(1)
	}
}
