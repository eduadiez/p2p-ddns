package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/crypto/sha3"
	iaddr "github.com/ipfs/go-ipfs-addr"
	logging "github.com/ipfs/go-log"
	"github.com/libp2p/go-libp2p"
	crypto "github.com/libp2p/go-libp2p-crypto"
	"github.com/libp2p/go-libp2p-discovery"
	libp2pdht "github.com/libp2p/go-libp2p-kad-dht"
	inet "github.com/libp2p/go-libp2p-net"
	pstore "github.com/libp2p/go-libp2p-peerstore"
	"github.com/libp2p/go-libp2p-protocol"
	multiaddr "github.com/multiformats/go-multiaddr"
)

// IPFS bootstrap nodes. Used to find other peers in the network.
var defaultBootstrapAddrStrings = []string{
	"/ip4/104.131.131.82/tcp/4001/ipfs/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
	"/ip4/104.236.179.241/tcp/4001/ipfs/QmSoLPppuBtQSGwKDZT2M73ULpjvfd3aZ6ha4oFGL1KrGM",
	"/ip4/104.236.76.40/tcp/4001/ipfs/QmSoLV4Bbm51jM9C4gDYZQ9Cy3U6aXMJDAbzgu2fzaDs64",
	"/ip4/128.199.219.111/tcp/4001/ipfs/QmSoLSafTMBsPKadTEgaXctDQVcqN88CNLHXMkTNwMKPnu",
	"/ip4/178.62.158.247/tcp/4001/ipfs/QmSoLer265NRgSp2LA3dPaeykiS1J6DifTC88f5uVQKNAd",
}

// Account struct
type Account struct {
	Address string `json:"address"`
	PubKey  string `json:"pubKey"`
	PrvKey  string `json:"prvKey"`
}

func main() {

	sourcePort := 20202
	RendezvousString := "Hi!!"
	ProtocolID := "/chat/1.1.0"

	ctx := context.Background()

	var prvKey crypto.PrivKey
	var pubKey crypto.PubKey

	var acc Account

	filedata, err := ioutil.ReadFile("key.txt")
	if err != nil {
		prvKey, pubKey, _ = crypto.GenerateKeyPair(crypto.Secp256k1, 0)
		prvKeyRaw, _ := prvKey.Raw()
		pubKeyRaw, _ := pubKey.Raw()
		address := Keccak256(pubKeyRaw[1:])[12:]
		acc = Account{hex.EncodeToString(address[:]), hex.EncodeToString(pubKeyRaw), hex.EncodeToString(prvKeyRaw)}
		JSONaccount, _ := json.Marshal(acc)
		fmt.Printf("%+v", string(JSONaccount))
		writeKeyFile("key.txt", JSONaccount)
	} else {
		json.Unmarshal(filedata, &acc)
		pk, _ := hex.DecodeString(acc.PrvKey)
		prvKey, err = crypto.UnmarshalSecp256k1PrivateKey(pk)
		pubKey = prvKey.GetPublic()
	}

	fmt.Printf("Account: 0x%s\n", acc.Address)
	fmt.Printf("Public key: %s\n", acc.PrvKey)
	fmt.Printf("Private Key: %s\n", acc.PrvKey)

	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", sourcePort))
	options := []libp2p.Option{
		libp2p.ListenAddrs(sourceMultiAddr),
		libp2p.Identity(prvKey),
	}

	// libp2p.New constructs a new libp2p Host.
	// Other options can be added here.
	host, err := libp2p.New(ctx, options...)
	if err != nil {
		panic(err)
	}
	fmt.Println("Host created. We are:", host.ID())
	fmt.Println("Addrs:", host.Addrs())

	// Set a function as stream handler.
	// This function is called when a peer initiates a connection and starts a stream with this peer.
	host.SetStreamHandler(protocol.ID(ProtocolID), handleStream)

	// Start a DHT, for use in peer discovery. We can't just make a new DHT client
	// because we want each peer to maintain its own local copy of the DHT, so
	// that the bootstrapping node of the DHT can go down without inhibitting
	// future peer discovery.
	kademliaDHT, err := libp2pdht.New(ctx, host)
	if err != nil {
		panic(err)
	}

	// Bootstrap the DHT. In the default configuration, this spawns a Background
	// thread that will refresh the peer table every five minutes.
	fmt.Println("Bootstrapping the DHT")
	if err = kademliaDHT.Bootstrap(ctx); err != nil {
		panic(err)
	}

	// Let's connect to the bootstrap nodes first. They will tell us about the other nodes in the network.
	for _, peerAddr := range defaultBootstrapAddrStrings {
		addr, _ := iaddr.ParseString(peerAddr)
		peerinfo, _ := pstore.InfoFromP2pAddr(addr.Multiaddr())

		if err := host.Connect(ctx, *peerinfo); err != nil {
			fmt.Println(err)
		} else {
			fmt.Println("Connection established with bootstrap node:", *peerinfo)
		}
	}

	// We use a rendezvous point "meet me here" to announce our location.
	// This is like telling your friends to meet you at the Eiffel Tower.
	fmt.Println("Announcing ourselves...")
	routingDiscovery := discovery.NewRoutingDiscovery(kademliaDHT)
	discovery.Advertise(ctx, routingDiscovery, RendezvousString)
	fmt.Println("Successfully announced!")

	// Now, look for others who have announced
	// This is like your friend telling you the location to meet you.
	fmt.Println("Searching for other peers...")
	peerChan, err := routingDiscovery.FindPeers(ctx, RendezvousString)
	if err != nil {
		panic(err)
	}

	go func() {
		for peer := range peerChan {
			if peer.ID == host.ID() {
				continue
			}
			fmt.Println("Found peer:", peer)

			fmt.Println("Connecting to:", peer)
			stream, err := host.NewStream(ctx, peer.ID, protocol.ID(ProtocolID))

			if err != nil {
				fmt.Println("Connection failed:", err)
				continue
			} else {
				rw := bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream))

				//go writeData(rw)
				go readData(rw)

				_, err = rw.WriteString(fmt.Sprintf("%s\n", "Hey!! I need your IP!"))
				if err != nil {
					fmt.Println("Error writing to buffer")
					panic(err)
				}
				err = rw.Flush()
				if err != nil {
					fmt.Println("Error flushing buffer")
					panic(err)
				}
			}

			fmt.Println("Connected to:", peer)
		}
	}()

	select {}
}

// Keccak256 func
func Keccak256(data ...[]byte) []byte {
	d := sha3.NewKeccak256()
	for _, b := range data {
		d.Write(b)
	}
	return d.Sum(nil)
}

var log = logging.Logger("rendezvous")

func handleStream(stream inet.Stream) {
	log.Info("Got a new stream!")
	fmt.Println("Got a new stream!")
	// Create a buffer stream for non blocking read and write.
	//rw := bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream))

	//go readData(rw)
	//go writeData(rw)

	// 'stream' will stay open until you close it (or the other side closes it).
}

func readData(rw *bufio.ReadWriter) {
	for {
		str, err := rw.ReadString('\n')
		if err != nil {
			fmt.Println("Error reading from buffer")
			//panic(err)
		}

		if str == "" {
			return
		}
		if str != "\n" {
			// Green console colour: 	\x1b[32m
			// Reset console colour: 	\x1b[0m
			fmt.Printf("\x1b[32m%s\x1b[0m> ", str)
		}

	}
}

func writeData(rw *bufio.ReadWriter) {
	stdReader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("> ")
		sendData, err := stdReader.ReadString('\n')
		if err != nil {
			fmt.Println("Error reading from stdin")
			panic(err)
		}

		_, err = rw.WriteString(fmt.Sprintf("%s\n", sendData))
		if err != nil {
			fmt.Println("Error writing to buffer")
			panic(err)
		}
		err = rw.Flush()
		if err != nil {
			fmt.Println("Error flushing buffer")
			panic(err)
		}
	}
}

func writeKeyFile(file string, content []byte) error {
	dir, basename := filepath.Split(file)

	// Atomic write: create a temporary hidden file first
	// then move it into place. TempFile assigns mode 0600.
	f, err := ioutil.TempFile(dir, "."+basename+".tmp")
	if err != nil {
		return err
	}

	if _, err := f.Write(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// BUG(pascaldekloe): Windows won't allow updates to a keyfile when it is being read.
	return os.Rename(f.Name(), file)
}
