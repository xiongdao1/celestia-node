package ipld

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/gammazero/workerpool"
	format "github.com/ipfs/go-ipld-format"
	mdutils "github.com/ipfs/go-merkledag/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/pkg/da"
	"github.com/tendermint/tendermint/pkg/wrapper"

	"github.com/celestiaorg/nmt"
	"github.com/celestiaorg/rsmt2d"
)

func init() {
	// randomize quadrant fetching, otherwise quadrant sampling is deterministic
	rand.Seed(time.Now().UnixNano())
	// limit the amount of workers for tests
	pool = workerpool.New(1000)
}

func TestRetriever_Retrieve(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bServ := mdutils.Bserv()
	r := NewRetriever(bServ)

	type test struct {
		name       string
		squareSize int
	}
	tests := []test{
		{"1x1(min)", 1},
		{"2x2(med)", 2},
		{"4x4(med)", 4},
		{"8x8(med)", 8},
		{"16x16(med)", 16},
		{"32x32(med)", 32},
		{"64x64(med)", 64},
		{"128x128(max)", MaxSquareSize},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// generate EDS
			shares := RandShares(t, tc.squareSize*tc.squareSize)
			in, err := AddShares(ctx, shares, bServ)
			require.NoError(t, err)

			// limit with timeout, specifically retrieval
			ctx, cancel := context.WithTimeout(ctx, time.Minute*5) // the timeout is big for the max size which is long
			defer cancel()

			dah := da.NewDataAvailabilityHeader(in)
			out, err := r.Retrieve(ctx, &dah)
			require.NoError(t, err)
			assert.True(t, EqualEDS(in, out))
		})
	}
}

func TestRetriever_ByzantineError(t *testing.T) {
	const width = 8
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	bserv := mdutils.Bserv()
	shares := ExtractEDS(RandEDS(t, width))
	_, err := ImportShares(ctx, shares, bserv)
	require.NoError(t, err)

	// corrupt shares so that eds erasure coding does not match
	copy(shares[14][8:], shares[15][8:])

	// import corrupted eds
	batchAdder := NewNmtNodeAdder(ctx, bserv, format.MaxSizeBatchOption(batchSize(width*2)))
	tree := wrapper.NewErasuredNamespacedMerkleTree(uint64(width), nmt.NodeVisitor(batchAdder.Visit))
	attackerEDS, err := rsmt2d.ImportExtendedDataSquare(shares, DefaultRSMT2DCodec(), tree.Constructor)
	require.NoError(t, err)
	err = batchAdder.Commit()
	require.NoError(t, err)

	// ensure we rcv an error
	da := da.NewDataAvailabilityHeader(attackerEDS)
	r := NewRetriever(bserv)
	_, err = r.Retrieve(ctx, &da)
	var errByz *ErrByzantine
	require.ErrorAs(t, err, &errByz)
}

// TestRetriever_MultipleRandQuadrants asserts that reconstruction succeeds
// when any three random quadrants requested.
func TestRetriever_MultipleRandQuadrants(t *testing.T) {
	RetrieveQuadrantTimeout = time.Millisecond * 500
	const squareSize = 32
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	bServ := mdutils.Bserv()
	r := NewRetriever(bServ)

	// generate EDS
	shares := RandShares(t, squareSize*squareSize)
	in, err := AddShares(ctx, shares, bServ)
	require.NoError(t, err)

	dah := da.NewDataAvailabilityHeader(in)
	ses, err := r.newSession(ctx, &dah)
	require.NoError(t, err)

	// wait until two additional quadrants requested
	// this reliably allows us to reproduce the issue
	time.Sleep(RetrieveQuadrantTimeout * 2)
	// then ensure we have enough shares for reconstruction for slow machines e.g. CI
	<-ses.Done()

	_, err = ses.Reconstruct(ctx)
	assert.NoError(t, err)
}
