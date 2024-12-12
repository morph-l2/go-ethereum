package trie

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	zktrie "github.com/scroll-tech/zktrie/trie"
	zkt "github.com/scroll-tech/zktrie/types"
)

type Hbss2Pbss struct {
	zkTrie        *ZkTrie
	db            ethdb.Database
	wg            *sync.WaitGroup
	datadir       string
	trieCachePath string
	errChan       chan error
	headerBlock   *types.Block
}

func NewHbss2Pbss(chaindb ethdb.Database, datadir, trieCachePath string) (*Hbss2Pbss, error) {
	stateCache := NewDatabaseWithConfig(chaindb, &Config{
		Zktrie: true,
	})

	headBlock := rawdb.ReadHeadBlock(chaindb)
	root := headBlock.Root()
	log.Info("Hbss2pbss converting", "block number", headBlock.NumberU64(), "root", root.Hex(), "hash", headBlock.Header().Hash().Hex())
	zkTrie, err := NewZkTrie(root, stateCache)
	if err != nil {
		return nil, err
	}

	return &Hbss2Pbss{
		db:            chaindb,
		zkTrie:        zkTrie,
		datadir:       datadir,
		trieCachePath: trieCachePath,
		errChan:       make(chan error),
		wg:            &sync.WaitGroup{},
		headerBlock:   headBlock,
	}, nil
}

func (h2p *Hbss2Pbss) Run() error {
	if _, err := os.Stat(h2p.trieCachePath); os.IsNotExist(err) {
		log.Warn("The clean trie cache is not found.")
	} else {
		os.RemoveAll(h2p.trieCachePath)
		log.Info("Deleted trie clean cache", "path", h2p.trieCachePath)
	}

	// Write genesis in new state db
	err := h2p.handleGenesis()
	if err != nil {
		return err
	}

	// Convert hbss state db to new state db
	root, err := h2p.zkTrie.Tree().Root()
	if err != nil {
		return err
	}
	start := time.Now()
	go func() {
		defer func() {
			close(h2p.errChan)
		}()

		h2p.concurrentTraversal(root, []bool{}, common.Hash{})
		h2p.wg.Wait()
	}()

	for err := range h2p.errChan { // wait until p.errChan is closed, or an error occurs
		if err != nil {
			return err
		}
	}
	log.Info("Hbss2Pbss complete", "elapsed", common.PrettyDuration(time.Since(start)))

	rawdb.WritePersistentStateID(h2p.db, h2p.headerBlock.NumberU64())
	rawdb.WriteStateID(h2p.db, h2p.headerBlock.Root(), h2p.headerBlock.NumberU64())

	return nil
}

func (h2p *Hbss2Pbss) handleGenesis() error {
	genesisHash := rawdb.ReadCanonicalHash(h2p.db, 0)
	if genesisHash == (common.Hash{}) {
		return errors.New("missing genesis hash")
	}
	genesis := rawdb.ReadBlock(h2p.db, genesisHash, 0)
	if genesis == nil {
		return errors.New("missing genesis block")
	}
	genesisRoot := genesis.Root()

	log.Info("Hbss2Pbss converting genesis", "root", genesisRoot.Hex())

	h2p.concurrentTraversal(zkt.NewHashFromBytes(genesisRoot[:]), []bool{}, common.Hash{})

	// Mark genesis root state
	rawdb.WriteStateID(h2p.db, genesisRoot, 0)

	return nil
}

func (h2p *Hbss2Pbss) Compact() error {
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
		if err := h2p.db.Compact(start, end); err != nil {
			log.Error("Database compaction failed", "error", err)
			return err
		}
	}
	log.Info("Database compaction finished", "elapsed", common.PrettyDuration(time.Since(cstart)))

	return nil
}

func (h2p *Hbss2Pbss) writeNode(pathKey []bool, n *zktrie.Node, owner common.Hash) {
	if owner == (common.Hash{}) {
		rawdb.WriteAccountTrieNode(h2p.db, zkt.PathToKey(pathKey)[:], n.CanonicalValue())
		log.Debug("WriteNodes account trie node", "type", n.Type, "path", zkt.PathToString(pathKey))
	} else {
		rawdb.WriteStorageTrieNode(h2p.db, owner, zkt.PathToKey(pathKey)[:], n.CanonicalValue())
		log.Debug("WriteNodes storage trie node", "owner", owner.Hex(), "type", n.Type, "path", zkt.PathToString(pathKey))
	}
}

func (h2p *Hbss2Pbss) concurrentTraversal(nodeHash *zkt.Hash, path []bool, owner common.Hash) error {
	n, err := h2p.zkTrie.Tree().GetNode(nodeHash)
	if err != nil {
		return err
	}

	switch n.Type {
	case zktrie.NodeTypeEmpty, zktrie.NodeTypeEmpty_New:
		h2p.writeNode(path, n, owner)
		return nil
	case zktrie.NodeTypeLeaf_New:
		h2p.writeNode(path, n, owner)

		if bytes.Equal(owner[:], common.Hash{}.Bytes()) {
			data, err := types.UnmarshalStateAccount(n.Data())
			if err != nil {
				h2p.errChan <- err
				return err
			}

			if data.CodeSize > 0 {
				if !bytes.Equal(data.Root[:], common.Hash{}.Bytes()) {
					// log.Info("/-------------------\\")
					// log.Info("concurrentTraversal", "owner", owner.Hex(), "path", zkt.PathToString(path), "nodeHash", nodeHash.Hex(), "key", n.NodeKey.Hex(), "root", data.Root.Hex())

					// codeHash := data.KeccakCodeHash
					h2p.concurrentTraversal(zkt.NewHashFromBytes(data.Root[:]), []bool{}, common.BytesToHash(n.NodeKey[:]))
					// h2p.wg.Add(1)
					// go func(root, o common.Hash) {
					// 	defer h2p.wg.Done()
					// 	log.Info("/-------------------\\")
					// 	log.Info("concurrentTraversal", "owner", owner.Hex(), "path", zkt.PathToString(path), "nodeHash", nodeHash.Hex(), "key", n.NodeKey.Hex())
					// 	h2p.concurrentTraversal(zkt.NewHashFromBytes(root[:]), []bool{}, o)
					// 	log.Info("\\-------------------/")
					// }(data.Root, common.BytesToHash(n.NodeKey.Bytes()))
					// log.Info("\\-------------------/")
				}
			}
		}

		return nil
	case zktrie.NodeTypeBranch_0, zktrie.NodeTypeBranch_1, zktrie.NodeTypeBranch_2, zktrie.NodeTypeBranch_3:
		h2p.writeNode(path, n, owner)

		leftErr := h2p.concurrentTraversal(n.ChildL, append(path, false), owner)
		if leftErr != nil {
			h2p.errChan <- leftErr
			return leftErr
		}
		rightErr := h2p.concurrentTraversal(n.ChildR, append(path, true), owner)
		if rightErr != nil {
			h2p.errChan <- rightErr
			return rightErr
		}
	default:
		return errors.New(fmt.Sprint("unexpected node type", n.Type))
	}

	return nil
}
