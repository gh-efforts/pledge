package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	logging "github.com/ipfs/go-log/v2"
	"github.com/urfave/cli/v2"
)

var log = logging.Logger("pledge")

func main() {
	app := &cli.App{
		Name:                 "pledge",
		Usage:                "A tool for boost and lotus-miner pledge",
		Version:              "0.0.1",
		EnableBashCompletion: true,
		Before:               before,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "repo",
				Usage: "pledge repo path",
				Value: "~/.pledge",
			},
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "enable debug mode",
			},
		},
		Commands: []*cli.Command{
			initCmd,
			runCmd,
			marketAddCmd,
			walletCmd,
		},
	}

	app.Setup()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-c
		os.Exit(1)
	}()

	if err := app.Run(os.Args); err != nil {
		fmt.Printf("ERROR: %s\n\n", err) // nolint:errcheck
		os.Exit(1)
	}
}
