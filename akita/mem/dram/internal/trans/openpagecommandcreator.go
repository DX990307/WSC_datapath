package trans

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// OpenPageCommandCreator leaves the accessed DRAM row open so that later
// requests to the same row can reuse the activation.
type OpenPageCommandCreator struct {
	AddrMapper addressmapping.Mapper
}

// Create creates a read or write command without auto-precharge.
func (c *OpenPageCommandCreator) Create(
	subTrans *signal.SubTransaction,
) *signal.Command {
	cmd := &signal.Command{
		ID: sim.GetIDGenerator().Generate(),
	}

	if subTrans.IsRead() {
		cmd.Kind = signal.CmdKindRead
	} else {
		cmd.Kind = signal.CmdKindWrite
	}

	cmd.Location = c.AddrMapper.Map(subTrans.Address)
	cmd.SubTrans = subTrans

	return cmd
}
