package pathdb

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	dbtypes "github.com/morph-l2/go-ethereum/triedb/types"
)

var _ trienodebuffer = &asyncnodebuffer{}

// asyncnodebuffer implement trienodebuffer interface, and async the nodecache
// to disk.
type asyncnodebuffer struct {
	mux          sync.RWMutex
	current      *nodecache
	background   *nodecache
	isFlushing   atomic.Bool
	stopFlushing atomic.Bool
}

// newAsyncNodeBuffer initializes the async node buffer with the provided nodes.
func newAsyncNodeBuffer(limit int, nodes dbtypes.KvMap, layers uint64) *asyncnodebuffer {
	if nodes == nil {
		nodes = make(dbtypes.KvMap)
	}
	var size uint64
	for _, v := range nodes {
		size += uint64(len(v.K) + len(v.V))
	}

	return &asyncnodebuffer{
		current:    newNodeCache(uint64(limit), size, nodes, layers),
		background: newNodeCache(uint64(limit), 0, make(dbtypes.KvMap), 0),
	}
}

// node retrieves the trie node with given node info.
func (a *asyncnodebuffer) node(path []byte) ([]byte, error) {
	a.mux.RLock()
	defer a.mux.RUnlock()

	node, err := a.current.node(path)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return a.background.node(path)
	}
	return node, nil
}

// commit merges the dirty nodes into the nodebuffer. This operation won't take
// the ownership of the nodes map which belongs to the bottom-most diff layer.
// It will just hold the node references from the given map which are safe to
// copy.
func (a *asyncnodebuffer) commit(nodes dbtypes.KvMap) trienodebuffer {
	a.mux.Lock()
	defer a.mux.Unlock()

	err := a.current.commit(nodes)
	if err != nil {
		log.Crit("[BUG] Failed to commit nodes to asyncnodebuffer", "error", err)
	}
	return a
}

// setSize is unsupported in asyncnodebuffer, due to the double buffer, blocking will occur.
func (a *asyncnodebuffer) setSize(size int, db ethdb.KeyValueStore, clean *fastcache.Cache, id uint64) error {
	return errors.New("not supported")
}

// reset cleans up the disk cache.
func (a *asyncnodebuffer) reset() {
	a.mux.Lock()
	defer a.mux.Unlock()

	a.current.reset()
	a.background.reset()
}

// empty returns an indicator if nodebuffer contains any state transition inside.
func (a *asyncnodebuffer) empty() bool {
	a.mux.RLock()
	defer a.mux.RUnlock()

	return a.current.empty() && a.background.empty()
}

// flush persists the in-memory dirty trie node into the disk if the configured
// memory threshold is reached. Note, all data must be written atomically.
func (a *asyncnodebuffer) flush(db ethdb.KeyValueStore, clean *fastcache.Cache, id uint64, force bool) error {
	a.mux.Lock()
	defer a.mux.Unlock()

	if a.stopFlushing.Load() {
		return nil
	}

	if force {
		for {
			if atomic.LoadUint64(&a.background.immutable) == 1 {
				time.Sleep(time.Duration(DefaultBackgroundFlushInterval) * time.Second)
				log.Info("Waiting background memory table flushed into disk for forcing flush node buffer")
				continue
			}
			atomic.StoreUint64(&a.current.immutable, 1)
			return a.current.flush(db, clean, id)
		}
	}

	if a.current.size < a.current.limit {
		return nil
	}

	// background flush doing
	if atomic.LoadUint64(&a.background.immutable) == 1 {
		return nil
	}

	atomic.StoreUint64(&a.current.immutable, 1)
	a.current, a.background = a.background, a.current

	a.isFlushing.Store(true)
	go func(persistID uint64) {
		defer a.isFlushing.Store(false)
		for {
			err := a.background.flush(db, clean, persistID)
			if err == nil {
				log.Debug("Succeed to flush background nodecache to disk", "state_id", persistID)
				return
			}
			log.Error("Failed to flush background nodecache to disk", "state_id", persistID, "error", err)
		}
	}(id)
	return nil
}

func (a *asyncnodebuffer) waitAndStopFlushing() {
	a.stopFlushing.Store(true)
	for a.isFlushing.Load() {
		time.Sleep(time.Second)
		log.Warn("Waiting background memory table flushed into disk")
	}
}

func (a *asyncnodebuffer) getAllNodes() dbtypes.KvMap {
	a.mux.Lock()
	defer a.mux.Unlock()

	cached, err := a.current.merge(a.background)
	if err != nil {
		log.Crit("[BUG] Failed to merge node cache under revert async node buffer", "error", err)
	}
	return cached.nodes
}

func (a *asyncnodebuffer) getLayers() uint64 {
	a.mux.RLock()
	defer a.mux.RUnlock()

	return a.current.layers + a.background.layers
}

func (a *asyncnodebuffer) getSize() (uint64, uint64) {
	a.mux.RLock()
	defer a.mux.RUnlock()

	return a.current.size, a.background.size
}

type nodecache struct {
	layers    uint64        // The number of diff layers aggregated inside
	size      uint64        // The size of aggregated writes
	limit     uint64        // The maximum memory allowance in bytes
	nodes     dbtypes.KvMap // The dirty node set, mapped by owner and path
	immutable uint64        // The flag equal 1, flush nodes to disk background
}

func newNodeCache(limit, size uint64, nodes dbtypes.KvMap, layers uint64) *nodecache {
	return &nodecache{
		layers:    layers,
		size:      size,
		limit:     limit,
		nodes:     nodes,
		immutable: 0,
	}
}

func (nc *nodecache) node(path []byte) ([]byte, error) {
	n, ok := nc.nodes.Get(path)
	if ok {
		return n, nil
	}
	return nil, nil
}

func (nc *nodecache) commit(nodes dbtypes.KvMap) error {
	if atomic.LoadUint64(&nc.immutable) == 1 {
		return errWriteImmutable
	}

	var (
		delta         int64
		overwrite     int64
		overwriteSize int64
	)

	for _, v := range nodes {
		current, exist := nc.nodes.Get(v.K)
		if !exist {
			nc.nodes.Put(v.K, v.V)
			delta += int64(len(v.K) + len(v.V))

			continue
		}

		if !bytes.Equal(current, v.V) {
			delta += int64(len(v.V) - len(current))
			overwrite++
			overwriteSize += int64(len(v.V) + len(v.K))
		}

		nc.nodes.Put(v.K, v.V)
	}

	nc.updateSize(delta)
	nc.layers++
	gcNodesMeter.Mark(overwrite)
	gcBytesMeter.Mark(overwriteSize)
	return nil
}

func (nc *nodecache) updateSize(delta int64) {
	size := int64(nc.size) + delta
	if size >= 0 {
		nc.size = uint64(size)
		return
	}
	s := nc.size
	nc.size = 0
	log.Error("Invalid pathdb buffer size", "prev", common.StorageSize(s), "delta", common.StorageSize(delta))
}

func (nc *nodecache) reset() {
	atomic.StoreUint64(&nc.immutable, 0)
	nc.layers = 0
	nc.size = 0
	nc.nodes = make(dbtypes.KvMap)
}

func (nc *nodecache) empty() bool {
	return nc.layers == 0
}

func (nc *nodecache) flush(db ethdb.KeyValueStore, clean *fastcache.Cache, id uint64) error {
	if atomic.LoadUint64(&nc.immutable) != 1 {
		return errFlushMutable
	}

	// Ensure the target state id is aligned with the internal counter.
	head := rawdb.ReadPersistentStateID(db)
	if head+nc.layers != id {
		return fmt.Errorf("buffer layers (%d) cannot be applied on top of persisted state id (%d) to reach requested state id (%d)", nc.layers, head, id)
	}
	var (
		start = time.Now()
		batch = db.NewBatchWithSize(int(float64(nc.size) * DefaultBatchRedundancyRate))
	)
	nodes := writeNodes(batch, nc.nodes, clean)
	rawdb.WritePersistentStateID(batch, id)

	// Flush all mutations in a single batch
	size := batch.ValueSize()
	if err := batch.Write(); err != nil {
		return err
	}
	commitBytesMeter.Mark(int64(size))
	commitNodesMeter.Mark(int64(nodes))
	commitTimeTimer.UpdateSince(start)
	log.Debug("Persisted pathdb nodes", "nodes", len(nc.nodes), "bytes", common.StorageSize(size), "elapsed", common.PrettyDuration(time.Since(start)))
	nc.reset()
	return nil
}

func (nc *nodecache) merge(nc1 *nodecache) (*nodecache, error) {
	if nc == nil && nc1 == nil {
		return nil, nil
	}
	if nc == nil || nc.empty() {
		res := copyNodeCache(nc1)
		atomic.StoreUint64(&res.immutable, 0)
		return res, nil
	}
	if nc1 == nil || nc1.empty() {
		res := copyNodeCache(nc)
		atomic.StoreUint64(&res.immutable, 0)
		return res, nil
	}
	if atomic.LoadUint64(&nc.immutable) == atomic.LoadUint64(&nc1.immutable) {
		return nil, errIncompatibleMerge
	}

	var (
		immutable *nodecache
		mutable   *nodecache
		res       = &nodecache{}
	)
	if atomic.LoadUint64(&nc.immutable) == 1 {
		immutable = nc
		mutable = nc1
	} else {
		immutable = nc1
		mutable = nc
	}
	res.size = immutable.size + mutable.size
	res.layers = immutable.layers + mutable.layers
	res.limit = immutable.limit
	res.nodes = make(dbtypes.KvMap)
	for _, v := range immutable.nodes {
		res.nodes.Put(v.K, v.V)
	}

	for _, v := range mutable.nodes {
		res.nodes.Put(v.K, v.V)
	}

	return res, nil
}

func copyNodeCache(n *nodecache) *nodecache {
	if n == nil {
		return nil
	}
	nc := &nodecache{
		layers:    n.layers,
		size:      n.size,
		limit:     n.limit,
		immutable: atomic.LoadUint64(&n.immutable),
		nodes:     make(dbtypes.KvMap),
	}

	for _, v := range n.nodes {
		nc.nodes.Put(v.K, v.V)
	}

	return nc
}
