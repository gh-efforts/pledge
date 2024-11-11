package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"time"

	"github.com/filecoin-project/go-state-types/big"

	"github.com/filecoin-project/go-state-types/abi"

	"github.com/filecoin-project/boost/cli/node"
	cborutil "github.com/filecoin-project/go-cbor-util"
	"github.com/filecoin-project/go-commp-utils/writer"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/messagesigner"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/ipfs/boxo/blockservice"
	bstore "github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	ihelper "github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-cidutil"
	"github.com/ipfs/go-cidutil/cidenc"
	"github.com/ipfs/go-datastore"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	chunk "github.com/ipfs/go-ipfs-chunker"
	ipldformat "github.com/ipfs/go-ipld-format"
	"github.com/ipld/go-car/v2"
	"github.com/ipld/go-car/v2/blockstore"
	inet "github.com/libp2p/go-libp2p/core/network"
	"github.com/multiformats/go-multibase"
	"github.com/multiformats/go-multihash"
)

const (
	defaultHashFunction = uint64(multihash.BLAKE2B_MIN + 31)
	unixfsChunkSize     = uint64(1 << 20)
	unixfsLinksPerLevel = 1024
)

func CreateRandomFile(dir string, size int64) (string, error) {
	source := io.LimitReader(rand.New(rand.NewSource(time.Now().Unix())), size)
	file, err := os.CreateTemp(dir, "source.dat")
	if err != nil {
		return "", err
	}
	defer file.Close()

	_, err = io.Copy(file, source)
	if err != nil {
		return "", err
	}

	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return "", err
	}

	return file.Name(), nil
}

func CreateDenseCARv2(dir, src string) (cid.Cid, string, error) {
	cs := int64(unixfsChunkSize)
	maxLinks := unixfsLinksPerLevel
	carOpts := []car.Option{
		blockstore.UseWholeCIDs(true),
	}
	return CreateDenseCARWith(dir, src, cs, maxLinks, carOpts)
}

func CreateDenseCARWith(dir, src string, chunkSize int64, maxLinks int, carOpts []car.Option) (cid.Cid, string, error) {
	bs := bstore.NewBlockstore(dssync.MutexWrap(ds.NewMapDatastore()))
	dagSvc := merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))

	root, err := WriteUnixfsDAGTo(src, dagSvc, chunkSize, maxLinks)
	if err != nil {
		return cid.Undef, "", err
	}

	// Create a UnixFS DAG again AND generate a CARv2 file using a CARv2
	// read-write blockstore now that we have the root.
	out, err := os.CreateTemp(dir, "rand")
	if err != nil {
		return cid.Undef, "", err
	}
	err = out.Close()
	if err != nil {
		return cid.Undef, "", err
	}

	rw, err := blockstore.OpenReadWrite(out.Name(), []cid.Cid{root}, carOpts...)
	if err != nil {
		return cid.Undef, "", err
	}

	dagSvc = merkledag.NewDAGService(blockservice.New(rw, offline.Exchange(rw)))

	root2, err := WriteUnixfsDAGTo(src, dagSvc, chunkSize, maxLinks)
	if err != nil {
		return cid.Undef, "", err
	}

	err = rw.Finalize()
	if err != nil {
		return cid.Undef, "", err
	}

	if root != root2 {
		return cid.Undef, "", fmt.Errorf("DAG root cid mismatch")
	}

	return root, out.Name(), nil
}

func WriteUnixfsDAGTo(path string, into ipldformat.DAGService, chunkSize int64, maxLinks int) (cid.Cid, error) {
	file, err := os.Open(path)
	if err != nil {
		return cid.Undef, err
	}
	defer file.Close() // nolint:errcheck

	stat, err := file.Stat()
	if err != nil {
		return cid.Undef, err
	}

	rpf, err := files.NewReaderPathFile(file.Name(), file, stat)
	if err != nil {
		return cid.Undef, err
	}

	prefix, err := merkledag.PrefixForCidVersion(1)
	if err != nil {
		return cid.Undef, err
	}

	prefix.MhType = defaultHashFunction

	bufferedDS := ipldformat.NewBufferedDAG(context.Background(), into)
	params := ihelper.DagBuilderParams{
		Maxlinks:  maxLinks,
		RawLeaves: true,
		// NOTE: InlineBuilder not recommended, we are using this to test identity CIDs
		CidBuilder: cidutil.InlineBuilder{
			Builder: prefix,
			Limit:   126,
		},
		Dagserv: bufferedDS,
		NoCopy:  true,
	}

	db, err := params.New(chunk.NewSizeSplitter(rpf, chunkSize))
	if err != nil {
		return cid.Undef, err
	}

	nd, err := balanced.Layout(db)
	if err != nil {
		return cid.Undef, err
	}

	err = bufferedDS.Commit()
	if err != nil {
		return cid.Undef, err
	}

	err = rpf.Close()
	if err != nil {
		return cid.Undef, err
	}

	return nd.Cid(), nil
}

type commpResult struct {
	CommPCid    string
	PieceSize   uint64
	CarFileSize int64
}

func commP(filePath string) (*commpResult, error) {
	start := time.Now()
	defer func() {
		log.Infow("calculate commP", "file", filePath, "duration", time.Since(start))
	}()
	rdr, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func(rdr *os.File) {
		err := rdr.Close()
		if err != nil {
			log.Errorf("close file %s error: %s", rdr.Name(), err)
		}
	}(rdr)

	w := &writer.Writer{}
	_, err = io.CopyBuffer(w, rdr, make([]byte, writer.CommPBuf))
	if err != nil {
		return nil, err
	}
	cp, err := w.Sum()
	if err != nil {
		return nil, err
	}

	encoder := cidenc.Encoder{Base: multibase.MustNewEncoder(multibase.Base32)}

	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	return &commpResult{
		CommPCid:    encoder.Encode(cp.PieceCID),
		PieceSize:   uint64(cp.PieceSize.Unpadded().Padded()),
		CarFileSize: stat.Size(),
	}, nil
}

func doRpc(ctx context.Context, s inet.Stream, req interface{}, resp interface{}) error {
	errc := make(chan error)
	go func() {
		if err := cborutil.WriteCborRPC(s, req); err != nil {
			errc <- fmt.Errorf("failed to send request: %w", err)
			return
		}

		if err := cborutil.ReadCborRPC(s, resp); err != nil {
			errc <- fmt.Errorf("failed to read response: %w", err)
			return
		}

		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func SignAndPushToMpool(ctx context.Context, nodeAPI api.Gateway, n *node.Node, msg *types.Message) (cid cid.Cid, sent bool, err error) {
	messageSigner := messagesigner.NewMessageSigner(n.Wallet,
		&nonceAPI{nodeAPI},
		dssync.MutexWrap(datastore.NewMapDatastore()))

	head, err := nodeAPI.ChainHead(ctx)
	if err != nil {
		return
	}
	baseFee := head.Blocks()[0].ParentBaseFee

	spec := &api.MessageSendSpec{
		MaxFee: abi.NewTokenAmount(1000000000), // 1 nFIL
	}

	msg, err = nodeAPI.GasEstimateMessageGas(ctx, msg, spec, types.EmptyTSK)
	if err != nil {
		err = fmt.Errorf("GasEstimateMessageGas error: %w", err)
		return
	}

	// use baseFee + 20%
	newGasFeeCap := big.Mul(baseFee, big.NewInt(6))
	newGasFeeCap = big.Div(newGasFeeCap, big.NewInt(5))

	if big.Cmp(msg.GasFeeCap, newGasFeeCap) < 0 {
		msg.GasFeeCap = newGasFeeCap
	}

	smsg, err := messageSigner.SignMessage(ctx, msg, nil, func(*types.SignedMessage) error { return nil })
	if err != nil {
		return
	}

	cid, err = nodeAPI.MpoolPush(ctx, smsg)
	if err != nil {
		err = fmt.Errorf("mpool push: failed to push message: %w", err)
		return
	}

	sent = true
	return
}
