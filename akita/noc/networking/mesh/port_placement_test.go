package mesh_test

import (
	"testing"

	"github.com/sarchlab/akita/v3/noc/networking/mesh"
	"github.com/sarchlab/akita/v3/sim"
)

func newTestConnector() *mesh.Connector {
	c := mesh.NewConnector().WithEngine(sim.NewSerialEngine())
	c.CreateNetwork("Network")

	return c
}

// A port added to a second tile used to be re-plugged silently: its
// connection and routing entry went to whichever tile was built last.
func TestAddTileRejectsPortOnTwoTiles(t *testing.T) {
	c := newTestConnector()
	shared := sim.NewLimitNumMsgPort(nil, 1, "Shared")
	c.AddTile([3]int{0, 0, 0}, []sim.Port{shared})

	defer func() {
		if recover() == nil {
			t.Fatal("adding a port to a second tile should panic")
		}
	}()

	c.AddTile([3]int{1, 1, 0}, []sim.Port{sim.NewLimitNumMsgPort(nil, 1, "Other"), shared})
}

func TestTileOfAndEndPointOf(t *testing.T) {
	c := newTestConnector()
	a := sim.NewLimitNumMsgPort(nil, 1, "A")
	b := sim.NewLimitNumMsgPort(nil, 1, "B")
	c.AddTile([3]int{0, 0, 0}, []sim.Port{a})
	c.AddTile([3]int{2, 1, 0}, []sim.Port{b})
	c.EstablishNetwork()

	loc, ok := c.TileOf(b)
	if !ok || loc != [3]int{2, 1, 0} {
		t.Fatalf("TileOf(B) = %v, %v", loc, ok)
	}

	if b.Connection() == nil || b.Connection() != c.EndPointOf([3]int{2, 1, 0}) {
		t.Fatal("B must be connected to the endpoint of tile (2,1,0)")
	}

	if a.Connection() == b.Connection() {
		t.Fatal("ports on different tiles must not share an endpoint")
	}
}
