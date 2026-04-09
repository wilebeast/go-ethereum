package params

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

var (
	// MyGethSpecialAllocAddress is the hard-coded account that receives the extra
	// genesis allocation introduced by the customization.
	MyGethSpecialAllocAddress = common.HexToAddress("0x1234000000000000000000000000000000001337")
	// MyGethDonationAddress is the fixed beneficiary that receives the custom
	// per-block donation reward.
	MyGethDonationAddress = common.HexToAddress("0x4200000000000000000000000000000000000042")
	// MyGethSpecialAllocBalance is the genesis balance assigned to the special
	// account above. The value is expressed in wei.
	MyGethSpecialAllocBalance = new(big.Int).Mul(big.NewInt(424242), big.NewInt(Ether))
	// MyGethDonationStartBalance gives the donation account a visible balance from
	// block 0, so it exists in the genesis state before new blocks are mined.
	MyGethDonationStartBalance = new(big.Int).Mul(big.NewInt(1337), big.NewInt(Ether))
	// MyGethPerBlockDonationWei is the amount credited to the donation account on
	// every new block on the custom dev chain.
	MyGethPerBlockDonationWei = new(big.Int).Mul(big.NewInt(1), big.NewInt(Ether))
	// myGethDevChainID matches geth's built-in dev chain id.
	myGethDevChainID = big.NewInt(1337)
	// myGethTerminalTotalDifficulty == 0 identifies a chain that is already
	// post-merge at genesis, which is how geth's built-in dev chain is configured.
	myGethTerminalTotalDifficulty = big.NewInt(0)
)

// IsMyGethDevChain keeps the custom reward logic scoped to the built-in dev
// chain instead of leaking into unrelated networks.
func IsMyGethDevChain(config *ChainConfig) bool {
	// The config must exist, the chain id must be 1337, and the chain must be
	// merged from genesis (TTD == 0).
	return config != nil &&
		config.ChainID != nil &&
		config.ChainID.Cmp(myGethDevChainID) == 0 &&
		config.TerminalTotalDifficulty != nil &&
		config.TerminalTotalDifficulty.Cmp(myGethTerminalTotalDifficulty) == 0
}
