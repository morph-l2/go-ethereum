package bundlepool

import (
	"container/heap"
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
)

func createTestBundle(price int64) *types.Bundle {
	return &types.Bundle{
		Price: big.NewInt(price),
		Txs:   types.Transactions{},
	}
}

func TestBundleHeap_PushPop(t *testing.T) {
	bundleHeap := BundleHeap{}

	// Push bundles into the heap
	bundle1 := createTestBundle(100)
	bundle0 := createTestBundle(20)
	bundle2 := createTestBundle(200)
	bundle3 := createTestBundle(50)

	heap.Push(&bundleHeap, bundle1)
	heap.Push(&bundleHeap, bundle0)
	heap.Push(&bundleHeap, bundle2)
	heap.Push(&bundleHeap, bundle3)

	assert.Equal(t, 4, bundleHeap.Len(), "Heap should contain 4 bundles")

	// Check the top of the heap (should be the lowest price)
	top := bundleHeap[0]
	assert.Equal(t, int64(20), top.Price.Int64(), "Top bundle should have the lowest price")

	// Pop bundles from the heap (should be in ascending order of price)
	// popped := bundleHeap.Pop().(*types.Bundle)
	popped := heap.Pop(&bundleHeap).(*types.Bundle)
	assert.Equal(t, int64(20), popped.Price.Int64(), "First popped bundle should have the lowest price")

	popped = heap.Pop(&bundleHeap).(*types.Bundle)
	assert.Equal(t, int64(50), popped.Price.Int64(), "Second popped bundle should have the second lowest price")

	popped = heap.Pop(&bundleHeap).(*types.Bundle)
	assert.Equal(t, int64(100), popped.Price.Int64(), "Third popped bundle should have the highest price")

	popped = heap.Pop(&bundleHeap).(*types.Bundle)
	assert.Equal(t, int64(200), popped.Price.Int64(), "Fourth popped bundle should have the highest price")

	assert.Equal(t, 0, bundleHeap.Len(), "Heap should be empty after popping all bundles")
}

func TestBundleHeap_Less(t *testing.T) {
	bundleHeap := BundleHeap{}

	// Add bundles to the heap
	bundle1 := createTestBundle(100)
	bundle2 := createTestBundle(200)

	heap.Push(&bundleHeap, bundle1)
	heap.Push(&bundleHeap, bundle2)

	// Test the Less function
	assert.True(t, bundleHeap.Less(0, 1), "Bundle with lower price should come first")
	assert.False(t, bundleHeap.Less(1, 0), "Bundle with higher price should not come first")
}

func TestBundleHeap_Swap(t *testing.T) {
	bundleHeap := BundleHeap{}

	// Add bundles to the heap
	bundle1 := createTestBundle(100)
	bundle2 := createTestBundle(200)

	heap.Push(&bundleHeap, bundle1)
	heap.Push(&bundleHeap, bundle2)

	// Swap the bundles
	bundleHeap.Swap(0, 1)

	assert.Equal(t, int64(200), (bundleHeap)[0].Price.Int64(), "First bundle should now have the higher price")
	assert.Equal(t, int64(100), (bundleHeap)[1].Price.Int64(), "Second bundle should now have the lower price")
}

func TestBundleHeap_Len(t *testing.T) {
	bundleHeap := &BundleHeap{}

	// Add bundles to the heap
	bundle1 := createTestBundle(100)
	bundle2 := createTestBundle(200)

	heap.Push(bundleHeap, bundle1)
	heap.Push(bundleHeap, bundle2)

	assert.Equal(t, 2, bundleHeap.Len(), "Heap should contain 2 bundles")

	// Pop a bundle and check the length
	heap.Pop(bundleHeap)
	assert.Equal(t, 1, bundleHeap.Len(), "Heap should contain 1 bundle after popping one")
}
