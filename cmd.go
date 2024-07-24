package main

import (
	logging "github.com/ipfs/go-log/v2"

	"github.com/urfave/cli/v2"
)

const DealProtocolv120 = "/fil/storage/mk/1.2.0"

func before(cctx *cli.Context) error {
	_ = logging.SetLogLevel("pledge", "INFO")
	if cctx.Bool("debug") {
		_ = logging.SetLogLevel("pledge", "DEBUG")
	}
	return nil
}
