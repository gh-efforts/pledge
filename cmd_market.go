package main

import (
	"fmt"

	"github.com/filecoin-project/boost/cli/node"
	"github.com/filecoin-project/boost/cmd"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/chain/actors"
	marketactor "github.com/filecoin-project/lotus/chain/actors/builtin/market"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/urfave/cli/v2"
)

var marketAddCmd = &cli.Command{
	Name:        "market-add",
	Usage:       "Add funds to the Storage Market actor",
	Description: "Send signed message to add funds for the default wallet to the Storage Market actor. Uses 2x current BaseFee and a maximum fee of 1 nFIL. This is an experimental utility, do not use in production.",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "wallet",
			Usage: "move balance from this wallet address to its market actor",
		},
	},
	ArgsUsage: "<amount>",
	Before:    before,
	Action: func(cctx *cli.Context) error {
		if !cctx.Args().Present() {
			return fmt.Errorf("must pass amount to add")
		}
		f, err := types.ParseFIL(cctx.Args().First())
		if err != nil {
			return fmt.Errorf("parsing 'amount' argument: %w", err)
		}

		amt := abi.TokenAmount(f)

		ctx := lcli.ReqContext(cctx)

		n, err := node.Setup(cctx.String(cmd.FlagRepo.Name))
		if err != nil {
			return err
		}

		api, closer, err := lcli.GetGatewayAPI(cctx)
		if err != nil {
			return fmt.Errorf("cant setup gateway connection: %w", err)
		}
		defer closer()

		walletAddr, err := n.GetProvidedOrDefaultWallet(ctx, cctx.String("wallet"))
		if err != nil {
			return err
		}

		log.Infow("selected wallet", "wallet", walletAddr)

		params, err := actors.SerializeParams(&walletAddr)
		if err != nil {
			return err
		}

		msg := &types.Message{
			To:     marketactor.Address,
			From:   walletAddr,
			Value:  amt,
			Method: marketactor.Methods.AddBalance,
			Params: params,
		}

		cid, sent, err := SignAndPushToMpool(ctx, api, n, msg)
		if err != nil {
			return err
		}
		if !sent {
			return nil
		}

		log.Infow("submitted market-add message", "cid", cid.String())

		return nil
	},
}
