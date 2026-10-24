package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	keycap "github.com/lengzhao/GoChainKit/cap/keystore"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/wallet"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gochainkit-wallet create|address|balance|send|sign|transaction|status [flags]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	directory := flags.String("keystore", "data/wallet-keys", "encrypted wallet directory")
	id := flags.String("key", "wallet", "wallet key ID")
	endpoint := flags.String("api", "http://127.0.0.1:8081", "node API URL")
	address := flags.String("address", "", "address for balance (optional) or recipient for send")
	amount := flags.Uint64("amount", 0, "transfer amount in integer base units")
	fee := flags.Uint64("fee", 0, "fee in integer base units")
	transaction := flags.String("id", "", "transaction ID")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	switch command {
	case "create", "address", "balance", "send", "sign", "transaction", "status":
	default:
		return fmt.Errorf("unknown command %q", command)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	print := func(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
	client, err := wallet.NewClient(*endpoint)
	if err != nil {
		return err
	}
	if command == "status" {
		var out any
		if err = client.Request(ctx, "GET", "/v1/chain/head", nil, &out); err != nil {
			return err
		}
		return print(out)
	}
	if command == "transaction" {
		data, err := hex.DecodeString(*transaction)
		if err != nil || len(data) != 32 {
			return fmt.Errorf("transaction ID must be 64 hex characters")
		}
		var out any
		if err = client.Request(ctx, "GET", "/v1/transactions/"+strings.ToLower(*transaction), nil, &out); err != nil {
			return err
		}
		return print(out)
	}
	if command == "balance" && *address != "" {
		owner, err := wallet.ParseAddress(*address)
		if err != nil {
			return err
		}
		out, err := client.Balance(ctx, owner)
		if err != nil {
			return err
		}
		return print(out)
	}
	store, err := keystore.NewEncryptedFile(keystore.FileConfig{Directory: *directory}, wallet.EnvironmentSecret{})
	if err != nil {
		return err
	}
	if command == "create" {
		owner, err := wallet.Create(ctx, store, keycap.ID(*id))
		if err != nil {
			return err
		}
		return print(map[string]string{"address": hex.EncodeToString(owner[:])})
	}
	key, err := wallet.Load(ctx, store, keycap.ID(*id))
	if err != nil {
		return err
	}
	defer clear(key)
	if command == "sign" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
		if err != nil {
			return err
		}
		signed, err := wallet.SignEncoded(key, data)
		if err != nil {
			return err
		}
		return print(signed)
	}
	owner := asset.AddressOf(key[32:])
	if command == "address" {
		return print(map[string]string{"address": hex.EncodeToString(owner[:])})
	}
	if command == "balance" {
		out, err := client.Balance(ctx, owner)
		if err != nil {
			return err
		}
		return print(out)
	}
	to, err := wallet.ParseAddress(*address)
	if err != nil {
		return err
	}
	out, err := client.Send(ctx, key, to, *amount, *fee)
	if err != nil {
		return err
	}
	return print(out)
}
