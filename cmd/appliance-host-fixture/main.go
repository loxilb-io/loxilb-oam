// Command appliance-host-fixture runs a stand-in for the Appliance host
// adapter on a Unix socket, so OAM's appliance API and the clients built on it
// can be developed before the real adapter exists.
//
// It executes nothing. See internal/appliance/hostfixture.
//
//	head -c 48 /dev/urandom | base64 > /tmp/appliance.key
//	appliance-host-fixture -socket /tmp/appliance.sock -key-file /tmp/appliance.key -available backup
//	OAM_APPLIANCE_HOST_SOCKET=/tmp/appliance.sock OAM_APPLIANCE_HOST_KEY_FILE=/tmp/appliance.key loxilb-oam ...
package main

import (
	"bytes"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/appliance"
	"github.com/loxilb-io/loxilb-oam/internal/appliance/hostfixture"
)

func main() {
	socketPath := flag.String("socket", "", "Unix socket to listen on (required)")
	keyFile := flag.String("key-file", "", "File holding the shared request-signing key (required)")
	available := flag.String("available", "", "Comma-separated actions to report as available: backup,restore,update,rollback,reset,diagnostics")
	flag.Parse()
	if *socketPath == "" || *keyFile == "" {
		flag.Usage()
		os.Exit(2)
	}

	key, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatalf("read key file: %v", err)
	}
	key = bytes.TrimSpace(key)
	if len(key) < appliance.MinKeyBytes {
		log.Fatalf("key must be at least %d bytes, got %d", appliance.MinKeyBytes, len(key))
	}

	var actions []appliance.Action
	for _, name := range strings.Split(*available, ",") {
		if name = strings.TrimSpace(name); name != "" {
			actions = append(actions, appliance.Action(name))
		}
	}

	// A socket left behind by a previous run would make Listen fail.
	if err := os.Remove(*socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("remove stale socket: %v", err)
	}
	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	if err := os.Chmod(*socketPath, 0o660); err != nil {
		log.Fatalf("chmod socket: %v", err)
	}

	server := &http.Server{Handler: hostfixture.New(key, actions...).Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		server.Close()
	}()

	log.Printf("FIXTURE host adapter listening on %s — it executes nothing; available actions: %v", *socketPath, actions)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
	os.Remove(*socketPath)
}
