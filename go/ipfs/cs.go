package ipfs

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"

	cid "gx/ipfs/QmTprEaAA2A9bst5XH7exuyi5KzNMK3SEDNN8rBDnKWcUS/go-cid"
	mh "gx/ipfs/QmU9a9NV9RdPNwZQDYd5uKsm6N6LJLSvLbywDDYFbaaC6P/go-multihash"
	blocks "gx/ipfs/QmVA4mafxbfH5aEvNz8fyoxC6J1xhAtw88B4GerPznSZBg/go-block-format"

	"github.com/attic-labs/noms/go/chunks"
	"github.com/attic-labs/noms/go/d"
	"github.com/attic-labs/noms/go/hash"
	"github.com/ipfs/go-ipfs/blocks/blockstore"
	"github.com/ipfs/go-ipfs/blockservice"
	"github.com/ipfs/go-ipfs/core"
	"github.com/ipfs/go-ipfs/repo"
	"github.com/ipfs/go-ipfs/repo/config"
	"github.com/ipfs/go-ipfs/repo/fsrepo"
)

// NewChunkStore creates a new ChunkStore backed by IPFS.
//
// Noms chunks written to this ChunkStore are converted to IPFS blocks and
// stored in an IPFS BlockStore.
//
// IPFS database specs have the form:
//   ipfs://<path-to-ipfs-dir>
// where 'ipfs' indicates the noms protocol and the path indicates the path to
// the directory where the ipfs repo resides. The chunkstore creates two files
// in the ipfs directory called 'noms' and 'noms-local' which stores the root
// of the noms database. This should ideally be done with IPNS, but that is
// currently too slow to be practical.
//
// This function creates an IPFS repo at the appropriate path if one doesn't
// already exist. If the global NodeIndex variable has been set to a number
// between 0 and 9 inclusive, the api, http, and swarm ports will be modified to
// end in with that digit. (e.g. if NodeIndex == 3, API port will be set to 5003)
//
// If local is true, only the local IPFS blockstore is used for both reads and
// write. If local is false, then reads will fall through to the network and
// blocks stored will be exposed to the entire IPFS network.
func NewChunkStore(p string, local bool) *chunkStore {
	node := OpenIPFSRepo(p, -1)
	return ChunkStoreFromIPFSNode(p, local, node)
}

// Creates a new chunchStore using a pre-existing IpfsNode. This is currently
// used to create a second 'local' chunkStore using the same IpfsNode as another
// non-local chunkStore.
func ChunkStoreFromIPFSNode(p string, local bool, node *core.IpfsNode) *chunkStore {
	return &chunkStore{
		node:      node,
		name:      p,
		local:     local,
		rateLimit: make(chan struct{}, 1024),
	}
}

// Opens a pre-existing ipfs repo for use as a noms store. This function will
// create a new IPFS repos at this indictated path if one doesn't already exist.
// Also if portIdx is a number between 0 and 1 inclusive, the config file will
// be modified to change external facing port numbers to end in 'portIdx'.
func OpenIPFSRepo(p string, portIdx int) *core.IpfsNode {
	r, err := fsrepo.Open(p)
	if _, ok := err.(fsrepo.NoRepoError); ok {
		var conf *config.Config
		conf, err = config.Init(os.Stdout, 2048)
		d.Chk.NoError(err)
		err = fsrepo.Init(p, conf)
		d.Chk.NoError(err)
		r, err = fsrepo.Open(p)
	}
	d.CheckError(err)

	resetRepoConfigPorts(r, portIdx)

	cfg := &core.BuildCfg{
		Repo:   r,
		Online: true,
		ExtraOpts: map[string]bool{
			"pubsub": true,
		},
	}

	node, err := core.NewNode(context.Background(), cfg)
	d.CheckError(err)
	return node
}

type chunkStore struct {
	root      *hash.Hash
	node      *core.IpfsNode
	name      string
	rateLimit chan struct{}
	local     bool
	test      bool
}

func (cs *chunkStore) RateLimitAdd() {
	cs.rateLimit <- struct{}{}
	cs.test = true
}

func (cs *chunkStore) RateLimitSub() {
	cs.test = false
	<-cs.rateLimit
}

func (cs *chunkStore) Get(h hash.Hash) chunks.Chunk {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs.RateLimitAdd()
	defer cs.RateLimitSub()

	var b blocks.Block
	var err error
	c := nomsHashToCID(h)
	if cs.local {
		b, err = cs.node.Blockstore.Get(c)
		if err == blockstore.ErrNotFound {
			return chunks.EmptyChunk
		}
	} else {
		b, err = cs.node.Blocks.GetBlock(ctx, c)
		if err == blockservice.ErrNotFound {
			return chunks.EmptyChunk
		}
	}
	d.PanicIfError(err)

	return chunks.NewChunkWithHash(h, b.RawData())
}

func (cs *chunkStore) GetMany(hashes hash.HashSet, foundChunks chan *chunks.Chunk) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs.RateLimitAdd()
	defer cs.RateLimitSub()

	cids := make([]*cid.Cid, 0, len(hashes))
	for h := range hashes {
		c := nomsHashToCID(h)
		cids = append(cids, c)
	}

	if cs.local {
		for _, cid := range cids {
			b, err := cs.node.Blockstore.Get(cid)
			d.PanicIfError(err)
			c := chunks.NewChunkWithHash(cidToNomsHash(b.Cid()), b.RawData())
			foundChunks <- &c
		}
	} else {
		for b := range cs.node.Blocks.GetBlocks(ctx, cids) {
			c := chunks.NewChunkWithHash(cidToNomsHash(b.Cid()), b.RawData())
			foundChunks <- &c
		}
	}
}

func (cs *chunkStore) Has(h hash.Hash) bool {
	cs.RateLimitAdd()
	defer cs.RateLimitSub()

	id := nomsHashToCID(h)
	if cs.local {
		ok, err := cs.node.Blockstore.Has(id)
		d.PanicIfError(err)
		return ok
	} else {
		// BlockService doesn't have Has(), neither does underlying Exchange()
		c := cs.Get(h)
		return !c.IsEmpty()
	}
}

func (cs *chunkStore) HasMany(hashes hash.HashSet) hash.HashSet {
	misses := hash.HashSet{}
	if cs.local {
		for h := range hashes {
			if !cs.Has(h) {
				misses[h] = struct{}{}
			}
		}
	} else {
		mu := sync.Mutex{}
		wg := sync.WaitGroup{}
		wg.Add(len(hashes))
		for h := range hashes {
			go func() {
				cs.RateLimitAdd()
				defer cs.RateLimitSub()
				defer wg.Done()
				ok := cs.Has(h)
				if !ok {
					mu.Lock()
					misses[h] = struct{}{}
					mu.Unlock()
				}
			}()
		}
	}
	return misses
}

func nomsHashToCID(nh hash.Hash) *cid.Cid {
	mhb, err := mh.Encode(nh[:], mh.SHA2_512)
	d.PanicIfError(err)
	return cid.NewCidV1(cid.Raw, mhb)
}

func (cs *chunkStore) Put(c chunks.Chunk) {
	cs.RateLimitAdd()
	defer cs.RateLimitSub()

	cid := nomsHashToCID(c.Hash())
	b, err := blocks.NewBlockWithCid(c.Data(), cid)
	d.PanicIfError(err)
	if cs.local {
		err = cs.node.Blockstore.Put(b)
		d.PanicIfError(err)
	} else {
		cid2, err := cs.node.Blocks.AddBlock(b)
		d.PanicIfError(err)
		d.PanicIfFalse(reflect.DeepEqual(cid, cid2))
	}
}

func (cs *chunkStore) Version() string {
	// TODO: Store this someplace in the DB root
	return "7.15"
}

func (cs *chunkStore) Rebase() {
	h := hash.Hash{}
	var sp string
	f := cs.getLocalNameFile(cs.local)
	b, err := ioutil.ReadFile(f)
	if !os.IsNotExist(err) {
		d.PanicIfError(err)
		sp = string(b)
	}

	if sp != "" {
		cid, err := cid.Decode(sp)
		d.PanicIfError(err)
		h = cidToNomsHash(cid)
	}
	cs.root = &h
}

func (cs *chunkStore) Root() (h hash.Hash) {
	if cs.root == nil {
		cs.Rebase()
	}
	return *cs.root
}

func cidToNomsHash(id *cid.Cid) (h hash.Hash) {
	dmh, err := mh.Decode([]byte(id.Hash()))
	d.PanicIfError(err)
	copy(h[:], dmh.Digest)
	return
}

func (cs *chunkStore) Commit(current, last hash.Hash) bool {
	// TODO: In a more realistic implementation this would flush queued chunks to storage.
	if cs.root != nil && *cs.root == current {
		fmt.Println("eep, asked to commit current value?")
		return true
	}

	// TODO: Optimistic concurrency?

	cid := nomsHashToCID(current)
	if cs.local {
		err := ioutil.WriteFile(cs.getLocalNameFile(true), []byte(cid.String()), 0644)
		d.PanicIfError(err)
	}
	err := ioutil.WriteFile(cs.getLocalNameFile(false), []byte(cid.String()), 0644)
	d.PanicIfError(err)

	cs.root = &current
	return true
}

func (cs *chunkStore) getLocalNameFile(local bool) string {
	if local {
		return path.Join(cs.name, "noms-local")
	}
	return path.Join(cs.name, "noms")
}

func (cs *chunkStore) Stats() interface{} {
	return nil
}

func (cs *chunkStore) Close() error {
	return cs.node.Close()
}

func resetRepoConfigPorts(r repo.Repo, nodeIdx int) {
	if nodeIdx < 0 || nodeIdx > 9 {
		return
	}

	apiPort := fmt.Sprintf("500%d", nodeIdx)
	gatewayPort := fmt.Sprintf("808%d", nodeIdx)
	swarmPort := fmt.Sprintf("400%d", nodeIdx)

	rc, err := r.Config()
	d.CheckError(err)

	rc.Addresses.API = strings.Replace(rc.Addresses.API, "5001", apiPort, -1)
	rc.Addresses.Gateway = strings.Replace(rc.Addresses.Gateway, "8080", gatewayPort, -1)
	for i, addr := range rc.Addresses.Swarm {
		rc.Addresses.Swarm[i] = strings.Replace(addr, "4001", swarmPort, -1)
	}
	err = r.SetConfig(rc)
	d.CheckError(err)
}
