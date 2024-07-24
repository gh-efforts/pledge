package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/filecoin-project/boost/cli/node"
	"github.com/filecoin-project/boost/cmd"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/urfave/cli/v2"
	"golang.org/x/term"
)

var walletCmd = &cli.Command{
	Name:  "wallet",
	Usage: "Manage wallets with Boost",
	Subcommands: []*cli.Command{
		walletExport,
		walletImport,
	},
}

var walletExport = &cli.Command{
	Name:      "export",
	Usage:     "export keys",
	ArgsUsage: "[address]",
	Action: func(cctx *cli.Context) error {
		ctx := lcli.ReqContext(cctx)

		n, err := node.Setup(cctx.String("repo"))
		if err != nil {
			return err
		}

		afmt := NewAppFmt(cctx.App)

		if !cctx.Args().Present() {
			err := fmt.Errorf("must specify key to export")
			return err
		}

		addr, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		ki, err := n.Wallet.WalletExport(ctx, addr)
		if err != nil {
			return err
		}

		b, err := json.Marshal(ki)
		if err != nil {
			return err
		}

		if cctx.Bool("json") {
			out := map[string]interface{}{
				"key": hex.EncodeToString(b),
			}
			return cmd.PrintJson(out)
		} else {
			afmt.Println(hex.EncodeToString(b))
		}
		return nil
	},
}

var walletImport = &cli.Command{
	Name:      "import",
	Usage:     "import keys",
	ArgsUsage: "[<path> (optional, will read from stdin if omitted)]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "format",
			Usage: "specify input format for key",
			Value: "hex-lotus",
		},
		&cli.BoolFlag{
			Name:  "as-default",
			Usage: "import the given key as your new default key",
		},
	},
	Action: func(cctx *cli.Context) error {
		ctx := lcli.ReqContext(cctx)

		n, err := node.Setup(cctx.String("repo"))
		if err != nil {
			return err
		}

		var inpdata []byte
		if !cctx.Args().Present() || cctx.Args().First() == "-" {
			if term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Print("Enter private key(not display in the terminal): ")

				sigCh := make(chan os.Signal, 1)
				// Notify the channel when SIGINT is received
				signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

				go func() {
					<-sigCh
					fmt.Println("\nInterrupt signal received. Exiting...")
					os.Exit(1)
				}()

				inpdata, err = term.ReadPassword(int(os.Stdin.Fd()))
				if err != nil {
					return err
				}
				fmt.Println()
			} else {
				reader := bufio.NewReader(os.Stdin)
				indata, err := reader.ReadBytes('\n')
				if err != nil {
					return err
				}
				inpdata = indata
			}

		} else {
			fdata, err := os.ReadFile(cctx.Args().First())
			if err != nil {
				return err
			}
			inpdata = fdata
		}

		var ki types.KeyInfo
		switch cctx.String("format") {
		case "hex-lotus":
			data, err := hex.DecodeString(strings.TrimSpace(string(inpdata)))
			if err != nil {
				return err
			}

			if err := json.Unmarshal(data, &ki); err != nil {
				return err
			}
		case "json-lotus":
			if err := json.Unmarshal(inpdata, &ki); err != nil {
				return err
			}
		case "gfc-json":
			var f struct {
				KeyInfo []struct {
					PrivateKey []byte
					SigType    int
				}
			}
			if err := json.Unmarshal(inpdata, &f); err != nil {
				return fmt.Errorf("failed to parse go-filecoin key: %s", err)
			}

			gk := f.KeyInfo[0]
			ki.PrivateKey = gk.PrivateKey
			switch gk.SigType {
			case 1:
				ki.Type = types.KTSecp256k1
			case 2:
				ki.Type = types.KTBLS
			default:
				return fmt.Errorf("unrecognized key type: %d", gk.SigType)
			}
		default:
			return fmt.Errorf("unrecognized format: %s", cctx.String("format"))
		}

		addr, err := n.Wallet.WalletImport(ctx, &ki)
		if err != nil {
			return err
		}

		if cctx.Bool("as-default") {
			if err := n.Wallet.SetDefault(addr); err != nil {
				return fmt.Errorf("failed to set default key: %w", err)
			}
		}

		if cctx.Bool("json") {
			out := map[string]interface{}{
				"address": addr,
			}
			return cmd.PrintJson(out)
		} else {
			fmt.Printf("imported key %s successfully!\n", addr)
		}
		return nil
	},
}
