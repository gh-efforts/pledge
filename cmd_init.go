package main

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/filecoin-project/boost/cli/node"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/mitchellh/go-homedir"
	"github.com/urfave/cli/v2"
)

var initCmd = &cli.Command{
	Name:   "init",
	Usage:  "Initialise Pledge repo",
	Before: before,
	Action: initAction,
}

func initAction(cctx *cli.Context) error {
	ctx := lcli.ReqContext(cctx)

	dir, err := homedir.Expand(cctx.String("repo"))
	if err != nil {
		return err
	}

	// Create the repo directory and temp directory
	if err := os.MkdirAll(path.Join(dir, "temp"), 0755); err != nil {
		return err
	}

	n, err := node.Setup(cctx.String("repo"))
	if err != nil {
		return err
	}

	nodeAPI, closer, err := lcli.GetGatewayAPI(cctx)
	if err != nil {
		return fmt.Errorf("cant setup gateway connection: %w", err)
	}
	defer closer()

	walletAddr, err := n.Wallet.GetDefault()
	if err != nil {
		return err
	}

	log.Infow("default wallet set", "wallet", walletAddr)

	walletBalance, err := nodeAPI.WalletBalance(ctx, walletAddr)
	if err != nil {
		return err
	}

	log.Infow("wallet balance", "value", types.FIL(walletBalance).Short())

	marketBalance, err := nodeAPI.StateMarketBalance(ctx, walletAddr, types.EmptyTSK)
	if err != nil {
		if strings.Contains(err.Error(), "actor not found") {
			log.Warn("market actor is not initialised, you must add funds to it in order to send online deals")
			return nil
		}
		return err
	}
	log.Infow("market balance", "escrow", types.FIL(marketBalance.Escrow).Short(), "locked", types.FIL(marketBalance.Locked).Short())

	return nil
}
