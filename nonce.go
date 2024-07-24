package main

import (
	"context"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/messagepool"
	"github.com/filecoin-project/lotus/chain/types"
)

type nonceAPI struct {
	nodeAPI api.Gateway
}

func (n *nonceAPI) GetNonce(ctx context.Context, address address.Address, key types.TipSetKey) (uint64, error) {
	act, err := n.nodeAPI.StateGetActor(ctx, address, key)
	if err != nil {
		return 0, err
	}
	return act.Nonce + 1, nil
}

func (n *nonceAPI) GetActor(ctx context.Context, address address.Address, key types.TipSetKey) (*types.Actor, error) {
	return n.nodeAPI.StateGetActor(ctx, address, key)
}

var _ messagepool.MpoolNonceAPI = &nonceAPI{}
