package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/lengzhao/GoChainKit/assembly"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"

	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "presets/single.yaml", "node configuration")
	duration := flag.Duration("run-for", 0, "optional bounded development run")
	flag.Parse()
	if *duration < 0 {
		return fmt.Errorf("run-for must not be negative")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	root, result, err := assembly.Load(ctx, file)
	if err != nil {
		return err
	}
	networks := map[string]networkcap.Network{}
	for _, instance := range result.Instances {
		if network, ok := instance.Value.(networkcap.Network); ok {
			networks[instance.ID] = network
		}
	}
	if len(networks) == 0 {
		return root.Run(ctx)
	}
	finished := make(chan error, 1)
	go func() { finished <- root.Run(ctx) }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var ticks <-chan time.Time = ticker.C
	for {
		select {
		case err := <-finished:
			return err
		case <-ticks:
			for id, network := range networks {
				addresses := network.Addresses()
				if len(addresses) > 0 {
					for _, addr := range addresses {
						fmt.Fprintln(os.Stdout, "Listening:", addr)
					}
					delete(networks, id)
				}
			}
			if len(networks) == 0 {
				ticker.Stop()
				ticks = nil
			}
		}
	}
}
