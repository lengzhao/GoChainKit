package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/lengzhao/GoChainKit/devnet"
	"github.com/lengzhao/GoChainKit/wallet"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	command := "init"
	args := os.Args[1:]
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	directory := flags.String("directory", "/network", "network configuration directory")
	data := flags.String("data-root", "/data", "node data root")
	endpoint := flags.String("api", "http://127.0.0.1:8080", "health check API")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch command {
	case "init":
		m, err := devnet.Initialize(ctx, devnet.Config{Directory: *directory, DataRoot: *data})
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	case "health":
		client, err := wallet.NewClient(*endpoint)
		if err != nil {
			return err
		}
		var out any
		return client.Request(ctx, "GET", "/v1/chain/head", nil, &out)
	default:
		return fmt.Errorf("usage: gochainkit-devnet init|health [flags]")
	}
}
