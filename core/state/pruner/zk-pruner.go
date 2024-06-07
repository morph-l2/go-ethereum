package pruner

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/trie"
	zktrie "github.com/scroll-tech/zktrie/trie"
	zkt "github.com/scroll-tech/zktrie/types"
)

type ZKPruner struct {
	db            ethdb.Database
	stateBloom    *stateBloom
	datadir       string
	trieCachePath string
	zkTrie        *trie.ZkTrie
	extractErr    chan error
	wg            *sync.WaitGroup
}

func NewZKPruner(chaindb ethdb.Database, bloomSize uint64, datadir, trieCachePath string) (*ZKPruner, error) {
	// Sanitize the bloom filter size if it's too small.
	if bloomSize < 256 {
		log.Warn("Sanitizing bloomfilter size", "provided(MB)", bloomSize, "updated(MB)", 256)
		bloomSize = 256
	}
	bloom, err := newStateBloomWithSize(bloomSize)
	if err != nil {
		return nil, err
	}

	stateCache := state.NewDatabaseWithConfig(chaindb, &trie.Config{
		Zktrie: true,
	})

	headBlock := rawdb.ReadHeadBlock(chaindb)
	root := headBlock.Root()
	log.Info("current head block", "block number", headBlock.NumberU64(), "root", root.Hex())
	zkTrie, err := trie.NewZkTrie(root, trie.NewZktrieDatabaseFromTriedb(stateCache.TrieDB()))
	if err != nil {
		return nil, err
	}

	return &ZKPruner{
		db:            chaindb,
		stateBloom:    bloom,
		zkTrie:        zkTrie,
		datadir:       datadir,
		trieCachePath: trieCachePath,

		extractErr: make(chan error),
		wg:         &sync.WaitGroup{},
	}, nil
}

func (p *ZKPruner) Prune(root common.Hash) error {
	// Before start the pruning, delete the clean trie cache first.
	// It's necessary otherwise in the next restart we will hit the
	// deleted state root in the "clean cache" so that the incomplete
	// state is picked for usage.
	deleteCleanTrieCache(p.trieCachePath)

	var targetRoot *common.Hash
	if root != (common.Hash{}) {
		_, err := p.zkTrie.Tree().GetNode(zkt.NewHashFromBytes(root[:]))
		if err == zktrie.ErrKeyNotFound {
			return fmt.Errorf("associated state[%x] is not present", root)
		} else if err != nil {
			return err
		}
		targetRoot = &root
		log.Info("Target root is specified", "root", root.Hex())
	}

	genesisHash := rawdb.ReadCanonicalHash(p.db, 0)
	if genesisHash == (common.Hash{}) {
		return errors.New("missing genesis hash")
	}
	genesis := rawdb.ReadBlock(p.db, genesisHash, 0)
	if genesis == nil {
		return errors.New("missing genesis block")
	}
	genesisRoot := genesis.Root()

	start := time.Now()
	log.Info("Start to extract nodes belongs to genesis trie and target trie")
	go func() {
		defer func() {
			close(p.extractErr)
		}()

		// Traverse the genesis, put all genesis state entries into the
		// bloom filter.
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.extractTrieNodes(&genesisRoot, true)
		}()

		// Traverse the target state, put all target state entries into the
		// bloom filter.
		p.extractTrieNodes(targetRoot, true)

		p.wg.Wait() // wait for all the extraction jobs to complete
	}()

	for err := range p.extractErr { // wait until p.extractErr is closed, or an error occurs
		if err != nil {
			return err
		}
	}
	log.Info("All nodes are extracted", "elapsed", common.PrettyDuration(time.Since(start)))

	filterName := bloomFilterName(p.datadir, root)
	log.Info("Writing state bloom to disk", "name", filterName)

	if err := p.stateBloom.Commit(filterName, filterName+stateBloomFileTempSuffix); err != nil {
		return err
	}

	return p.prune(start, filterName)
}

func (p *ZKPruner) prune(start time.Time, bloomPath string) error {
	var (
		count  int
		size   common.StorageSize
		pstart = time.Now()
		logged = time.Now()
		batch  = p.db.NewBatch()
		iter   = p.db.NewIterator(nil, nil)
	)
	for iter.Next() {
		key := iter.Key()
		// All state entries don't belong to specific state and genesis are deleted here
		// - trie node
		// - legacy contract code
		// - new-scheme contract code
		isCode, codeKey := rawdb.IsCodeKey(key)
		if len(key) == common.HashLength || isCode {
			checkKey := key
			if isCode {
				checkKey = codeKey
			}

			if ok, err := p.stateBloom.Contain(checkKey); err != nil {
				return err
			} else if ok {
				continue
			}
			count += 1
			size += common.StorageSize(len(key) + len(iter.Value()))

			batch.Delete(key)

			if time.Since(logged) > 8*time.Second {
				log.Info("Pruning state data", "nodes", count, "size", size,
					"elapsed", common.PrettyDuration(time.Since(pstart)))
				logged = time.Now()
			}
			// Recreate the iterator after every batch commit in order
			// to allow the underlying compactor to delete the entries.
			if batch.ValueSize() >= ethdb.IdealBatchSize {
				batch.Write()
				batch.Reset()

				iter.Release()
				iter = p.db.NewIterator(nil, key)
			}
		}
	}

	if batch.ValueSize() > 0 {
		batch.Write()
		batch.Reset()
	}
	iter.Release()
	log.Info("Pruned state data", "nodes", count, "size", size, "elapsed", common.PrettyDuration(time.Since(pstart)))

	os.RemoveAll(bloomPath)
	// Start compactions, will remove the deleted data from the disk immediately.
	// Note for small pruning, the compaction is skipped.
	if count >= rangeCompactionThreshold {
		cstart := time.Now()
		for b := 0x00; b <= 0xf0; b += 0x10 {
			var (
				start = []byte{byte(b)}
				end   = []byte{byte(b + 0x10)}
			)
			if b == 0xf0 {
				end = nil
			}
			log.Info("Compacting database", "range", fmt.Sprintf("%#x-%#x", start, end), "elapsed", common.PrettyDuration(time.Since(cstart)))
			if err := p.db.Compact(start, end); err != nil {
				log.Error("Database compaction failed", "error", err)
				return err
			}
		}
		log.Info("Database compaction finished", "elapsed", common.PrettyDuration(time.Since(cstart)))
	}
	log.Info("State pruning successful", "pruned", size, "elapsed", common.PrettyDuration(time.Since(start)))
	return nil
}

func (p *ZKPruner) extractTrieNodes(root *common.Hash, accountTrie bool) {
	var (
		err     error
		zktRoot *zkt.Hash
		mt      = p.zkTrie.Tree()
	)
	if root != nil {
		zktRoot = zkt.NewHashFromBytes(root[:])
	}
	if err = mt.Walk(zktRoot, func(n *zktrie.Node) {
		nodeHash, err := n.NodeHash()
		if err != nil {
			p.extractErr <- err
			return
		}
		switch n.Type {
		case zktrie.NodeTypeLeaf_New:
			if accountTrie {
				data, err := types.UnmarshalStateAccount(n.Data())
				if err != nil {
					p.extractErr <- err
					return
				}
				// traverse storage trie nodes under the contract account
				if data.CodeSize > 0 {
					rawdb.WriteCode(p.stateBloom, common.BytesToHash(data.KeccakCodeHash), nil)
					if !bytes.Equal(data.Root[:], common.Hash{}.Bytes()) {
						p.wg.Add(1)
						go func(root common.Hash) {
							defer p.wg.Done()
							p.extractTrieNodes(&root, false)
						}(data.Root)
					}
				}
			}

			p.stateBloom.Put(trie.BitReverse(nodeHash[:]), nil)
		case zktrie.NodeTypeBranch_0, zktrie.NodeTypeBranch_1, zktrie.NodeTypeBranch_2, zktrie.NodeTypeBranch_3, zktrie.DBEntryTypeRoot:
			p.stateBloom.Put(trie.BitReverse(nodeHash[:]), nil)
		case zktrie.NodeTypeEmpty, zktrie.NodeTypeLeaf, zktrie.NodeTypeParent:
			panic("encounter unsupported deprecated node type")
		default:
		}
	}); err != nil {
		p.extractErr <- err
	}
}
