package main

import (
	"context"
	"fmt"

	"github.com/filecoin-project/boost/api/client"

	"github.com/filecoin-project/boost/node/repo"

	"math/rand"
	"os"
	"path"
	"strings"
	"time"

	"github.com/docker/go-units"
	"github.com/filecoin-project/boost/cli/node"
	"github.com/filecoin-project/boost/cmd"
	boostTypes "github.com/filecoin-project/boost/storagemarket/types"
	"github.com/filecoin-project/go-address"
	cborutil "github.com/filecoin-project/go-cbor-util"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/builtin/v9/market"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/google/uuid"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-cidutil/cidenc"
	inet "github.com/libp2p/go-libp2p/core/network"
	"github.com/mitchellh/go-homedir"
	"github.com/multiformats/go-multibase"
	"github.com/urfave/cli/v2"
)

var runCmd = &cli.Command{
	Name:   "run",
	Usage:  "Run pledge",
	Before: before,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "provider",
			Usage:    "storage provider on-chain address",
			Required: true,
		},
		&cli.StringFlag{
			Name:  "min-size",
			Usage: "min size of the car file",
			Value: "1GiB",
		},
		&cli.StringFlag{
			Name:  "max-size",
			Usage: "max size of the car file",
			Value: "31GiB",
		},
		&cli.StringFlag{
			Name:  "max-pledge",
			Usage: "max size of the pledge",
		},
		&cli.BoolFlag{
			Name:  "verified",
			Usage: "whether the deal funds should come from verified client data-cap",
			Value: false,
		},
		&cli.BoolFlag{
			Name:  "remove-unsealed-copy",
			Usage: "indicates that an unsealed copy of the sector in not required for fast retrieval",
			Value: false,
		},
		&cli.StringFlag{
			Name:  "wallet",
			Usage: "wallet address to be used to initiate the deal",
		},
		&cli.BoolFlag{
			Name:  "skip-ipni-announce",
			Usage: "indicates that deal index should not be announced to the IPNI(Network Indexer)",
			Value: false,
		},
		&cli.Int64Flag{
			Name:  "storage-price",
			Usage: "storage price in attoFIL per epoch per GiB",
			Value: 1,
		},
		&cli.IntFlag{
			Name:  "duration",
			Usage: "duration of the deal in epochs",
			Value: 518400, // default is 2880 * 180 == 180 days
		},
		&cli.IntFlag{
			Name:  "start-epoch-head-offset",
			Usage: "start epoch by when the deal should be proved by provider on-chain after current chain head",
		},
		&cli.IntFlag{
			Name:  "start-epoch",
			Usage: "start epoch by when the deal should be proved by provider on-chain",
		},
	},

	Action: runAction,
}

func runAction(cctx *cli.Context) error {
	ctx := lcli.ReqContext(cctx)

	carMinSize, err := units.RAMInBytes(cctx.String("min-size"))
	if err != nil {
		return fmt.Errorf("min size: %w", err)
	}
	carMaxSize, err := units.RAMInBytes(cctx.String("max-size"))
	if err != nil {
		return fmt.Errorf("max size: %w", err)
	}
	if carMinSize > carMaxSize {
		return fmt.Errorf("min size is greater than max size")
	}

	var maxPledge int64
	if cctx.IsSet("max-pledge") {
		maxPledge, err = units.RAMInBytes(cctx.String("max-pledge"))
		if err != nil {
			return fmt.Errorf("max pledge: %w", err)
		}
	}
	dir, err := homedir.Expand(cctx.String("repo"))
	if err != nil {
		return fmt.Errorf("repo: %w", err)
	}

	n, err := node.Setup(dir)
	if err != nil {
		return err
	}

	nodeAPI, closer, err := lcli.GetGatewayAPI(cctx)
	if err != nil {
		return fmt.Errorf("cant setup gateway connection: %w", err)
	}
	defer closer()

	walletAddr, err := n.GetProvidedOrDefaultWallet(ctx, cctx.String("wallet"))
	if err != nil {
		return err
	}

	marketBalance, err := nodeAPI.StateMarketBalance(ctx, walletAddr, types.EmptyTSK)
	if err != nil {
		return err
	}

	if marketBalance.Escrow.LessThan(types.FromFil(1)) {
		return fmt.Errorf("market balance is less than 1 FIL: %s", walletAddr)
	}

	carPath := path.Join(dir, "temp")
	var totalPledge int64

	if maxPledge > 0 {
		for totalPledge < maxPledge {
			// run pledge
			size := carMinSize + rand.Int63n(carMaxSize-carMinSize+1)
			if err := runPledge(ctx, cctx, nodeAPI, n, walletAddr, carPath, size); err != nil {
				return err
			}
			totalPledge += size
		}
	} else {
		size := carMinSize + rand.Int63n(carMaxSize-carMinSize+1)
		if err := runPledge(ctx, cctx, nodeAPI, n, walletAddr, carPath, size); err != nil {
			return err
		}
		totalPledge += size
	}
	log.Infow("total pledge", "value", totalPledge)
	return nil
}

func runPledge(ctx context.Context, cctx *cli.Context, api api.Gateway, n *node.Node, walletAddr address.Address, dir string, size int64) error {
	start := time.Now()

	log.Infof("create random file, size: %d", size)
	rf, err := CreateRandomFile(dir, size)
	if err != nil {
		return err
	}
	log.Debugw("create random file", "path", rf, "size", size, "duration", time.Since(start))

	defer func() {
		log.Debugw("remove random file", "path", rf)
		if err := os.Remove(rf); err != nil {
			log.Errorf("remove file %s error: %s", rf, err)
		}
	}()

	log.Infof("create car file from %s", rf)
	start1 := time.Now()
	root, cn, err := CreateDenseCARv2(dir, rf)
	if err != nil {
		return err
	}

	encoder := cidenc.Encoder{Base: multibase.MustNewEncoder(multibase.Base32)}
	rn := encoder.Encode(root)

	base := path.Dir(cn)
	np := path.Join(base, rn+".car")

	rootCid, err := cid.Parse(rn)
	if err != nil {
		return fmt.Errorf("failed to parse root cid: %w", err)
	}

	err = os.Rename(cn, np)
	if err != nil {
		return err
	}
	log.Infow("create car file", "path", np, "cid", rn, "duration", time.Since(start1))

	cp, err := commP(np)
	if err != nil {
		return err
	}

	pieceCid, err := cid.Parse(cp.CommPCid)
	if err != nil {
		return fmt.Errorf("failed to parse piece cid: %w", err)
	}

	maddr, err := address.NewFromString(cctx.String("provider"))
	if err != nil {
		return err
	}

	addrInfo, err := cmd.GetAddrInfo(ctx, api, maddr)
	if err != nil {
		return err
	}
	log.Debugw("storage provider", "id", addrInfo.ID, "multiaddrs", addrInfo.Addrs, "addr", maddr)

	if err := n.Host.Connect(ctx, *addrInfo); err != nil {
		return fmt.Errorf("failed to connect to peer %s: %w", addrInfo.ID, err)
	}
	x, err := n.Host.Peerstore().FirstSupportedProtocol(addrInfo.ID, DealProtocolv120)
	if err != nil {
		return fmt.Errorf("getting protocols for peer %s: %w", addrInfo.ID, err)
	}

	if len(x) == 0 {
		return fmt.Errorf("boost client cannot make a deal with storage provider %s because it does not support protocol version 1.2.0", maddr)
	}
	dealUuid := uuid.New()
	transfer := boostTypes.Transfer{}

	var providerCollateral abi.TokenAmount

	bounds, err := api.StateDealProviderCollateralBounds(ctx, abi.PaddedPieceSize(cp.PieceSize), false, types.EmptyTSK)
	if err != nil {
		return fmt.Errorf("node error getting collateral bounds: %w", err)
	}
	providerCollateral = big.Div(big.Mul(bounds.Min, big.NewInt(6)), big.NewInt(5)) // add 20%

	tipset, err := api.ChainHead(ctx)
	if err != nil {
		return fmt.Errorf("cannot get chain head: %w", err)
	}

	head := tipset.Height()
	log.Debugw("current block height", "number", head)

	var startEpoch abi.ChainEpoch

	if cctx.IsSet("start-epoch-head-offset") {
		startEpoch = head + abi.ChainEpoch(cctx.Int("start-epoch-head-offset"))
	} else if cctx.IsSet("start-epoch") {
		startEpoch = abi.ChainEpoch(cctx.Int("start-epoch"))
	} else {
		// default
		startEpoch = head + abi.ChainEpoch(5760) // head + 2 days
	}

	dp, err := dealProposal(ctx, n, walletAddr, rootCid, abi.PaddedPieceSize(cp.PieceSize), pieceCid, maddr, startEpoch, cctx.Int("duration"), false, providerCollateral, abi.NewTokenAmount(cctx.Int64("storage-price")))
	if err != nil {
		return fmt.Errorf("failed to create a deal proposal: %w", err)
	}

	dealParams := boostTypes.DealParams{
		DealUUID:           dealUuid,
		ClientDealProposal: *dp,
		DealDataRoot:       rootCid,
		IsOffline:          true,
		Transfer:           transfer,
		RemoveUnsealedCopy: cctx.Bool("remove-unsealed-copy"),
		SkipIPNIAnnounce:   cctx.Bool("skip-ipni-announce"),
	}
	log.Debugw("about to submit deal proposal", "uuid", dealUuid.String())

	s, err := n.Host.NewStream(ctx, addrInfo.ID, DealProtocolv120)
	if err != nil {
		return fmt.Errorf("failed to open stream to peer %s: %w", addrInfo.ID, err)
	}
	defer func(s inet.Stream) {
		err := s.Close()
		if err != nil {
			log.Errorf("failed to close stream: %s", err)
		}
	}(s)

	var resp boostTypes.DealResponse
	if err := doRpc(ctx, s, &dealParams, &resp); err != nil {
		return fmt.Errorf("send proposal rpc: %w", err)
	}

	if !resp.Accepted {
		return fmt.Errorf("deal proposal rejected: %s", resp.Message)
	}

	return importData(cctx, dealUuid.String(), np)
}

func dealProposal(ctx context.Context, n *node.Node, clientAddr address.Address, rootCid cid.Cid, pieceSize abi.PaddedPieceSize, pieceCid cid.Cid, minerAddr address.Address, startEpoch abi.ChainEpoch, duration int, verified bool, providerCollateral abi.TokenAmount, storagePrice abi.TokenAmount) (*market.ClientDealProposal, error) {
	endEpoch := startEpoch + abi.ChainEpoch(duration)
	// deal proposal expects total storage price for deal per epoch, therefore we
	// multiply pieceSize * storagePrice (which is set per epoch per GiB) and divide by 2^30
	storagePricePerEpochForDeal := big.Div(big.Mul(big.NewInt(int64(pieceSize)), storagePrice), big.NewInt(int64(1<<30)))
	l, err := market.NewLabelFromString(rootCid.String())
	if err != nil {
		return nil, err
	}
	proposal := market.DealProposal{
		PieceCID:             pieceCid,
		PieceSize:            pieceSize,
		VerifiedDeal:         verified,
		Client:               clientAddr,
		Provider:             minerAddr,
		Label:                l,
		StartEpoch:           startEpoch,
		EndEpoch:             endEpoch,
		StoragePricePerEpoch: storagePricePerEpochForDeal,
		ProviderCollateral:   providerCollateral,
	}

	buf, err := cborutil.Dump(&proposal)
	if err != nil {
		return nil, err
	}

	sig, err := n.Wallet.WalletSign(ctx, clientAddr, buf, api.MsgMeta{Type: api.MTDealProposal})
	if err != nil {
		return nil, fmt.Errorf("wallet sign failed: %w", err)
	}

	return &market.ClientDealProposal{
		Proposal:        proposal,
		ClientSignature: *sig,
	}, nil
}

func importData(cctx *cli.Context, id string, filePath string) error {
	_, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("opening file %s: %w", filePath, err)
	}

	var proposalCid *cid.Cid
	dealUuid, err := uuid.Parse(id)
	if err != nil {
		propCid, err := cid.Decode(id)
		if err != nil {
			return fmt.Errorf("could not parse '%s' as deal uuid or proposal cid", id)
		}
		proposalCid = &propCid
	}
	addr, headers, err := lcli.GetRawAPI(cctx, repo.Boost, "v0")
	if err != nil {
		return err
	}
	napi, closer, err := client.NewBoostRPCV0(cctx.Context, addr, headers)
	if err != nil {
		return err
	}
	defer closer()

	if proposalCid != nil {

		// Look up the deal in the boost database
		deal, err := napi.BoostDealBySignedProposalCid(cctx.Context, *proposalCid)
		if err != nil {
			// If the error is anything other than a Not Found error,
			// return the error
			if !strings.Contains(err.Error(), "not found") {
				return err
			}

			return fmt.Errorf("cannot find boost deal with proposal cid %s and legacy deals are no olnger supported", proposalCid)
		}

		// Get the deal UUID from the deal
		dealUuid = deal.DealUuid
	}

	// Deal proposal by deal uuid (v1.2.0 deal)
	rej, err := napi.BoostOfflineDealWithData(cctx.Context, dealUuid, filePath, true)
	if err != nil {
		return fmt.Errorf("failed to execute offline deal: %w", err)
	}
	if rej != nil && rej.Reason != "" {
		return fmt.Errorf("offline deal %s rejected: %s", dealUuid, rej.Reason)
	}
	log.Infof("Offline deal import for v1.2.0 deal %s scheduled for execution", dealUuid)
	return nil
}
