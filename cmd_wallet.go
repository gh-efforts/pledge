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

	"github.com/dustin/go-humanize"
	"github.com/filecoin-project/boost/cli/node"
	"github.com/filecoin-project/boost/cmd"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/filecoin-project/lotus/lib/tablewriter"
	"github.com/urfave/cli/v2"
	"golang.org/x/term"
)

var walletCmd = &cli.Command{
	Name:  "wallet",
	Usage: "Manage wallets with Boost",
	Subcommands: []*cli.Command{
		walletExport,
		walletImport,
		walletList,
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

var walletList = &cli.Command{
	Name:  "list",
	Usage: "List wallet address",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "addr-only",
			Usage:   "Only print addresses",
			Aliases: []string{"a"},
		},
		&cli.BoolFlag{
			Name:    "id",
			Usage:   "Output ID addresses",
			Aliases: []string{"i"},
		},
	},
	Action: func(cctx *cli.Context) error {
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

		afmt := NewAppFmt(cctx.App)

		addrs, err := n.Wallet.WalletList(ctx)
		if err != nil {
			return err
		}

		// Assume an error means no default key is set
		def, _ := n.Wallet.GetDefault()

		// Map Keys. Corresponds to the standard tablewriter output
		addressKey := "Address"
		idKey := "ID"
		balanceKey := "Balance"
		marketKey := "market" // for json only
		marketAvailKey := "Market(Avail)"
		marketLockedKey := "Market(Locked)"
		nonceKey := "Nonce"
		defaultKey := "Default"
		errorKey := "Error"
		dataCapKey := "DataCap"

		// One-to-one mapping between tablewriter keys and JSON keys
		tableKeysToJsonKeys := map[string]string{
			addressKey: strings.ToLower(addressKey),
			idKey:      strings.ToLower(idKey),
			balanceKey: strings.ToLower(balanceKey),
			marketKey:  marketKey, // only in JSON
			nonceKey:   strings.ToLower(nonceKey),
			defaultKey: strings.ToLower(defaultKey),
			errorKey:   strings.ToLower(errorKey),
			dataCapKey: strings.ToLower(dataCapKey),
		}

		// List of Maps whose keys are defined above. One row = one list element = one wallet
		var wallets []map[string]interface{}

		for _, addr := range addrs {
			if cctx.Bool("addr-only") {
				afmt.Println(addr.String())
			} else {
				a, err := api.StateGetActor(ctx, addr, types.EmptyTSK)
				if err != nil {
					if !strings.Contains(err.Error(), "actor not found") {
						wallet := map[string]interface{}{
							addressKey: addr,
							errorKey:   err,
						}
						wallets = append(wallets, wallet)
						continue
					}

					a = &types.Actor{
						Balance: big.Zero(),
					}
				}

				wallet := map[string]interface{}{
					addressKey: addr,
					balanceKey: types.FIL(a.Balance),
					nonceKey:   a.Nonce,
				}

				if cctx.Bool("json") {
					if addr == def {
						wallet[defaultKey] = true
					} else {
						wallet[defaultKey] = false
					}
				} else {
					if addr == def {
						wallet[defaultKey] = "X"
					}
				}

				if cctx.Bool("id") {
					id, err := api.StateLookupID(ctx, addr, types.EmptyTSK)
					if err != nil {
						wallet[idKey] = "n/a"
					} else {
						wallet[idKey] = id
					}
				}

				mbal, err := api.StateMarketBalance(ctx, addr, types.EmptyTSK)
				if err == nil {
					marketAvailValue := types.FIL(types.BigSub(mbal.Escrow, mbal.Locked))
					marketLockedValue := types.FIL(mbal.Locked)
					// structure is different for these particular keys so we have to distinguish the cases here
					if cctx.Bool("json") {
						wallet[marketKey] = map[string]interface{}{
							"available": marketAvailValue,
							"locked":    marketLockedValue,
						}
					} else {
						wallet[marketAvailKey] = marketAvailValue
						wallet[marketLockedKey] = marketLockedValue
					}
				}
				dcap, err := api.StateVerifiedClientStatus(ctx, addr, types.EmptyTSK)
				if err == nil {
					wallet[dataCapKey] = dcap
					if !cctx.Bool("json") && dcap == nil {
						wallet[dataCapKey] = "X"
					} else if dcap != nil {
						wallet[dataCapKey] = humanize.IBytes(dcap.Int.Uint64())
					}
				} else {
					wallet[dataCapKey] = "n/a"
					if cctx.Bool("json") {
						wallet[dataCapKey] = nil
					}
				}

				wallets = append(wallets, wallet)
			}
		}

		if !cctx.Bool("addr-only") {

			if cctx.Bool("json") {
				// get a new list of wallets with json keys instead of tablewriter keys
				var jsonWallets []map[string]interface{}
				for _, wallet := range wallets {
					jsonWallet := make(map[string]interface{})
					for k, v := range wallet {
						jsonWallet[tableKeysToJsonKeys[k]] = v
					}
					jsonWallets = append(jsonWallets, jsonWallet)
				}
				// then return this!
				return cmd.PrintJson(jsonWallets)
			} else {
				// Init the tablewriter's columns
				tw := tablewriter.New(
					tablewriter.Col(addressKey),
					tablewriter.Col(idKey),
					tablewriter.Col(balanceKey),
					tablewriter.Col(marketAvailKey),
					tablewriter.Col(marketLockedKey),
					tablewriter.Col(nonceKey),
					tablewriter.Col(defaultKey),
					tablewriter.NewLineCol(errorKey))
				// populate it with content
				for _, wallet := range wallets {
					tw.Write(wallet)
				}
				// return the corresponding string
				return tw.Flush(os.Stdout)
			}
		}

		return nil
	},
}
