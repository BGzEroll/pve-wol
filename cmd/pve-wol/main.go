package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"pve-wol/internal/config"
	"pve-wol/internal/pve"
	"pve-wol/internal/registry"
	"pve-wol/internal/wol"
)

var version = "dev"

type syncRequest struct {
	done chan error
}

func main() {
	if err := run(); err != nil {
		log.Printf("pve-wol stopped with error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "/etc/pve-wol/config.yaml", "path to the configuration file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	client, err := pve.New(cfg.PVE)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Printf("pve-wol starting")

	connections := make([]listenerConnection, 0, len(cfg.WOL.Listen))
	for _, address := range cfg.WOL.Listen {
		conn, listenErr := wol.Open(address)
		if listenErr != nil {
			for _, opened := range connections {
				opened.conn.Close()
			}
			return fmt.Errorf("listen on %s: %w", address, listenErr)
		}
		connections = append(connections, listenerConnection{address: address, conn: conn})
		log.Printf("listening for WoL on UDP %s", address)
	}

	reg := registry.New()
	debouncer := wol.NewDebouncer(cfg.WOL.Debounce.Value())
	syncRequests := make(chan syncRequest)
	var syncWG sync.WaitGroup
	syncWG.Add(1)
	go func() {
		defer syncWG.Done()
		syncLoop(ctx, client, reg, cfg.SyncInterval.Value(), syncRequests)
	}()

	listenerErrors := make(chan error, len(connections))
	var listenerWG sync.WaitGroup
	var handlerWG sync.WaitGroup
	for _, listener := range connections {
		listener := listener
		listenerWG.Add(1)
		go func() {
			defer listenerWG.Done()
			err := wol.Serve(ctx, listener.conn, func(mac string) {
				handlerWG.Add(1)
				go func() {
					defer handlerWG.Done()
					handleWOL(ctx, client, reg, debouncer, syncRequests, mac)
				}()
			})
			if err != nil && ctx.Err() == nil {
				listenerErrors <- fmt.Errorf("UDP %s: %w", listener.address, err)
			}
		}()
	}

	var runErr error
	select {
	case runErr = <-listenerErrors:
		cancel()
	case <-ctx.Done():
	}
	cancel()
	listenerWG.Wait()
	syncWG.Wait()
	handlerWG.Wait()
	if runErr != nil {
		return runErr
	}
	log.Printf("pve-wol stopped")
	return nil
}

type listenerConnection struct {
	address string
	conn    *net.UDPConn
}

func syncLoop(ctx context.Context, client *pve.Client, reg *registry.Registry, interval time.Duration, requests <-chan syncRequest) {
	connected := false
	runSync := func() error {
		log.Printf("syncing guests from PVE")
		guests, err := client.Sync(ctx)
		if err != nil {
			log.Printf("registry sync failed: %v", err)
			return err
		}
		if !connected {
			log.Printf("connected to PVE: %s", client.URL())
			connected = true
		}
		conflicts, stats := reg.Replace(guests)
		for _, conflict := range conflicts {
			log.Printf("warning: MAC conflict %s; WoL start disabled for %s", conflict.MAC, formatCandidates(conflict.Candidates))
		}
		log.Printf("registry updated: %d guests, %d MAC addresses", stats.Guests, stats.MACs)
		return nil
	}

	runSync()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			err := runSync()
			request.done <- err
		case <-ticker.C:
			runSync()
		}
	}
}

func handleWOL(ctx context.Context, client *pve.Client, reg *registry.Registry, debouncer *wol.Debouncer, syncRequests chan<- syncRequest, mac string) {
	if !debouncer.Allow(mac) {
		log.Printf("duplicate WOL ignored: %s", mac)
		return
	}

	entry, ok := reg.Lookup(mac)
	if !ok {
		done := make(chan error, 1)
		request := syncRequest{done: done}
		select {
		case syncRequests <- request:
		case <-ctx.Done():
			return
		}
		if err := waitForSync(ctx, done); err != nil && ctx.Err() == nil {
			log.Printf("immediate registry sync failed: %v", err)
		}
		entry, ok = reg.Lookup(mac)
	}
	if !ok {
		log.Printf("unknown WOL target: %s", mac)
		return
	}
	if entry.Conflict {
		log.Printf("warning: ambiguous WOL target %s; refusing to start %s", mac, formatCandidates(entry.Candidates))
		return
	}

	guest := entry.Guest
	log.Printf("WOL received: %s -> %s/%d %s", mac, guest.Type, guest.VMID, guest.Name)
	if strings.EqualFold(guest.Status, "running") {
		log.Printf("guest already running, ignoring WOL")
		return
	}

	if err := client.Start(ctx, guest); err != nil {
		var apiErr *pve.APIError
		if errors.As(err, &apiErr) && apiErr.AlreadyRunning() {
			log.Printf("warning: guest already running, ignoring WOL: %s/%d %v", guest.Type, guest.VMID, err)
			reg.MarkRunning(guest)
			return
		}
		log.Printf("failed to start %s/%d: %v", guest.Type, guest.VMID, err)
		return
	}
	reg.MarkRunning(guest)
	log.Printf("started %s/%d %s", guest.Type, guest.VMID, guest.Name)
}

func waitForSync(ctx context.Context, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func formatCandidates(candidates []pve.Guest) string {
	parts := make([]string, 0, len(candidates))
	for _, guest := range candidates {
		parts = append(parts, fmt.Sprintf("%s/%d %s", guest.Type, guest.VMID, guest.Name))
	}
	return strings.Join(parts, ", ")
}
