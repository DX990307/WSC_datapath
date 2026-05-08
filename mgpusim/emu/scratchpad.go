package emu

import "github.com/sarchlab/mgpusim/v3/utils"

// Scratchpad is a piece of pure memory that is used by the ALU to store input
// and output data. It aliases utils.Scratchpad so timing/wavefront can use the
// same type without importing emu and creating an import cycle with sampling.
type Scratchpad = utils.Scratchpad

type SOP2Layout = utils.SOP2Layout
