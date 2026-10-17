package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/lengzhao/GoChainKit/assembly"
	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"os"
	"os/signal"
	"syscall"
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
	root, _, err := assembly.Load(ctx, file)
	if err != nil {
		return err
	}
	return root.Run(ctx)
}
