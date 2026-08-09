package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/imattau/frg/core/genesis"
	"github.com/imattau/frg/core/keys"
)

const configTemplate = `[node]
keypair_path = "frg.key"
db_path = "frg.db"
genesis_path = "genesis.json"

[p2p]
listen = "/ip4/0.0.0.0/tcp/%d"
peers = [%s
]
enable_mdns = true

[grpc]
listen = "0.0.0.0:%d"

[consensus]
propose_timeout_ms = 3000
prevote_timeout_ms = 3000
precommit_timeout_ms = 3000

chain_id = "%s"
`

type devNode struct {
	index   int
	kp      *keys.Keypair
	peerID  string
	p2pPort int
	grpcPort int
}

func main() {
	validators := flag.Int("validators", 7, "number of validator nodes")
	outputDir := flag.String("output-dir", "devnet-data", "output directory")
	chainID := flag.String("chain-id", "frg-devnet-1", "chain identifier")
	bond := flag.String("bond", "1000", "bond amount per validator")
	balance := flag.String("balance", "10000", "initial balance per validator")
	baseP2PPort := flag.Int("base-p2p-port", 17777, "starting P2P port (node 0)")
	baseGRPCPort := flag.Int("base-grpc-port", 50051, "starting gRPC port (node 0)")
	dockerCompose := flag.Bool("docker", true, "use /dns4/ scheme for peer addresses (Docker Compose)")
	flag.Parse()

	n := *validators
	if n < 1 {
		fmt.Fprintln(os.Stderr, "validators must be >= 1")
		os.Exit(1)
	}

	nodes := make([]devNode, n)
	for i := 0; i < n; i++ {
		kp, err := keys.GenerateKeypair()
		if err != nil {
			fmt.Fprintf(os.Stderr, "generate keypair for node %d: %v\n", i, err)
			os.Exit(1)
		}
		privKey, err := libp2pCrypto.UnmarshalEd25519PrivateKey(kp.PrivateKey[:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal libp2p key for node %d: %v\n", i, err)
			os.Exit(1)
		}
		pid, err := peer.IDFromPrivateKey(privKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "peer ID for node %d: %v\n", i, err)
			os.Exit(1)
		}
		nodes[i] = devNode{
			index:   i,
			kp:      kp,
			peerID:  pid.String(),
			p2pPort: *baseP2PPort + i,
			grpcPort: *baseGRPCPort + i,
		}
	}

	gen := genesis.Genesis{
		ChainID:    *chainID,
		Validators: make([]genesis.ValidatorEntry, n),
		Balances:   make([]genesis.BalanceEntry, n),
	}
	for i, nd := range nodes {
		pubHex := hex.EncodeToString(nd.kp.PublicKey[:])
		gen.Validators[i] = genesis.ValidatorEntry{PubKey: pubHex, Bond: *bond}
		gen.Balances[i] = genesis.BalanceEntry{Account: pubHex, Amount: *balance}
	}

	for _, nd := range nodes {
		dir := filepath.Join(*outputDir, fmt.Sprintf("node-%d", nd.index))
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", dir, err)
			os.Exit(1)
		}

		seed := kpToSeed(nd.kp)
		if err := os.WriteFile(filepath.Join(dir, "frg.key"), seed[:], 0600); err != nil {
			fmt.Fprintf(os.Stderr, "write key for node %d: %v\n", nd.index, err)
			os.Exit(1)
		}

		genData, err := json.MarshalIndent(gen, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal genesis for node %d: %v\n", nd.index, err)
			os.Exit(1)
		}
		genData = append(genData, '\n')
		if err := os.WriteFile(filepath.Join(dir, "genesis.json"), genData, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write genesis for node %d: %v\n", nd.index, err)
			os.Exit(1)
		}

		peers := buildPeerList(nodes, nd.index, *dockerCompose)
		cfg := fmt.Sprintf(configTemplate, nd.p2pPort, peers, nd.grpcPort, *chainID)
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write config for node %d: %v\n", nd.index, err)
			os.Exit(1)
		}
	}

	fmt.Printf("Devnet generated: %d nodes in %s/\n", n, *outputDir)
	for _, nd := range nodes {
		fmt.Printf("  node-%d  p2p=:%d  grpc=:%d  peer=%s\n", nd.index, nd.p2pPort, nd.grpcPort, nd.peerID)
	}

	if *dockerCompose {
		if err := writeDockerCompose(*outputDir, nodes); err != nil {
			fmt.Fprintf(os.Stderr, "write docker-compose: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Wrote docker-compose.yml\n")
	}
}

func kpToSeed(kp *keys.Keypair) [32]byte {
	priv := [64]byte(kp.PrivateKey)
	seed := [32]byte{}
	copy(seed[:], priv[:32])
	return seed
}

func buildPeerList(nodes []devNode, selfIdx int, dockerCompose bool) string {
	var lines string
	for _, nd := range nodes {
		if nd.index == selfIdx {
			continue
		}
		var addr string
		if dockerCompose {
			addr = fmt.Sprintf("/dns4/frg-node-%d/tcp/%d/p2p/%s", nd.index, nd.p2pPort, nd.peerID)
		} else {
			addr = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", nd.p2pPort, nd.peerID)
		}
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			continue
		}
		lines += fmt.Sprintf("\n  \"%s\",", ma.String())
	}
	return lines
}

func writeDockerCompose(outputDir string, nodes []devNode) error {
	var buf []byte
	buf = append(buf, "version: \"3.8\"\n\nservices:\n"...)

	for _, nd := range nodes {
		nodeDir := fmt.Sprintf("node-%d", nd.index)
		svc := fmt.Sprintf(`  %s:
    build:
      context: ../
      dockerfile: Dockerfile
    container_name: frg-node-%d
    working_dir: /data
    volumes:
      - ./%s:/data
    ports:
      - "%d:%d"
      - "%d:%d"
    restart: unless-stopped
`, fmt.Sprintf("frg-node-%d", nd.index), nd.index, nodeDir,
			nd.grpcPort, nd.grpcPort,
			nd.p2pPort, nd.p2pPort)
		buf = append(buf, svc...)
		if nd.index < len(nodes)-1 {
			buf = append(buf, '\n')
		}
	}

	return os.WriteFile(filepath.Join(outputDir, "docker-compose.yml"), buf, 0644)
}
